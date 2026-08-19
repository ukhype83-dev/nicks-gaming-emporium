package dbwriter

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"emporium/internal/catalog"
	"emporium/internal/customers"
	"emporium/internal/geography"
	"emporium/internal/hardware"
	"emporium/internal/shops"
	"emporium/internal/transactions"
	"emporium/internal/web"
)

// clickIDStride is the per-worker id range for the parallel clickstream load:
// worker w emits page_view_id / session_id starting at w*clickIDStride+1, so
// ranges are disjoint (unique PKs, sessions never merge across workers). 10^12
// per worker dwarfs any worker's row count (~2.8 B total at 3T ÷ workers) and
// stays far under BIGINT even at 16 workers (1.6×10^13 ≪ 9.2×10^18).
const clickIDStride int64 = 1_000_000_000_000

// writeBatchedIfEmpty streams n rows into schema.table via a BatchedLoader —
// ONE committed transaction per batch — iff the table is empty (resume-safe).
// This replaces loadIfEmpty's single all-rows BulkInsert, which runs as one
// transaction: fine for the narrow accounts table, but on the wide web.reviews
// (text bodies) that lone transaction overflowed nge's small (20 GB) log drive
// because SIMPLE recovery can only truncate COMMITTED log. Batched commits keep
// the active log bounded to one batch. Returns the resulting row count (the
// existing count when skipped).
func writeBatchedIfEmpty(ctx context.Context, w Writer, schema, table, idCol string, columns []string, n int, rowAt func(int) []any) (int64, error) {
	existing, err := w.MaxBigint(ctx, schema, table, idCol)
	if err != nil {
		return 0, err
	}
	if existing > 0 {
		return existing, nil // already loaded (resume)
	}
	bl := NewBatchedLoader(ctx, w, schema, table, columns, 0)
	for i := 0; i < n; i++ {
		if err := bl.Add(rowAt(i)); err != nil {
			return 0, err
		}
	}
	if err := bl.Flush(); err != nil {
		return 0, err
	}
	return bl.Total(), nil
}

// webWorkers resolves the worker count for the parallel web phases. An explicit
// vusers > 1 wins; otherwise default to NumCPU-2 (leave headroom for GC + the
// SQL client), clamped to [1, 12] so we never overwhelm the single SQL Server.
func webWorkers(vusers int) int {
	if vusers > 1 {
		if vusers > 16 {
			return 16
		}
		return vusers
	}
	n := runtime.NumCPU() - 2
	if n < 1 {
		n = 1
	}
	if n > 12 {
		n = 12
	}
	return n
}

// WebStats summarises a web-layer load.
type WebStats struct {
	Accounts  int64
	Reviews   int64
	Comments  int64
	Votes     int64
	PageViews int64
}

// LoadWeb is the V1.26 web-layer load — an ADDITIVE pass that adds accounts,
// reviews, comment threads, votes and clickstream to a database that already
// holds the customers/catalog/transactions they hang off. It never rewrites
// an existing row: the transaction replay (same seed → byte-identical) only
// reconstructs each reviewer's purchases in memory. The web.* tables must
// already exist (apply the schema, e.g. --init-schema, first).
//
// clickScale sizes the clickstream to the tier (the 3T daily curve × scale).
func LoadWeb(
	ctx context.Context,
	w Writer,
	dsn string,
	tier string,
	seed uint64,
	asOf time.Time,
	postals *geography.Index,
	cat *catalog.Index,
	hw *hardware.Index,
	bankDir string,
	clickScale float64,
	vusers int,
	progress func(string, int64),
) (WebStats, error) {
	var s WebStats
	if progress == nil {
		progress = func(string, int64) {}
	}
	workers := webWorkers(vusers)
	if err := ensureWebTables(ctx, w); err != nil {
		return s, err
	}

	banks, err := web.LoadBanks(bankDir)
	if err != nil {
		return s, fmt.Errorf("load web banks: %w", err)
	}
	e := web.NewEmitter(banks, seed, asOf, 1)

	// 1. Accounts (customer replay). Collect + write.
	var accounts []web.Account
	_, custIndex, err := customers.Generate(tier, seed, asOf, postals, func(c customers.Customer) {
		if a, ok := e.AccountFor(c); ok {
			accounts = append(accounts, a)
		}
	})
	if err != nil {
		return s, fmt.Errorf("customers replay: %w", err)
	}
	if s.Accounts, err = writeBatchedIfEmpty(ctx, w, "web", "accounts", "account_id", AccountColumns,
		len(accounts), func(i int) []any { return AccountRow(accounts[i]) }); err != nil {
		return s, fmt.Errorf("load web.accounts: %w", err)
	}
	progress("web.accounts", s.Accounts)

	// Release the account-build working set BEFORE the memory-heavy parallel
	// capture. The `accounts` slice (written now, or skipped on resume but still
	// materialised by the replay) and the username-uniqueness maps are all dead
	// from here on — capture/reviews join only through accountByCustomer +
	// accountList. Returning ~3 GB (at 3T) to the OS gives the capture shards
	// real headroom (they duplicate reviewer state across workers). Output is
	// byte-identical; this only reclaims memory.
	accounts = nil
	e.ReleaseAccountScratch()
	debug.FreeOSMemory()

	// 2. Reviewers + catalog lookups.
	e.MarkReviewers()
	e.SetCatalogs(buildReleaseMeta(cat), buildHardwareMeta(hw))

	// 3. Capture purchases via a PARALLEL transaction replay. Shops are
	//    sharded round-robin across `workers` goroutines; each replays its
	//    shard into a private CaptureShard (lock-free), then we merge
	//    deterministically. custIndex is the real one (identical to the base
	//    build → byte-identical per-shop lines → true verified-purchase
	//    coherence); nil staff is safe (independent RNG stream). No DB
	//    connection here — capture is pure CPU.
	shopList, err := shops.Generate(tier, seed, asOf, postals)
	if err != nil {
		return s, fmt.Errorf("shops.Generate: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  web: capturing purchases — %d workers × %d shops (round-robin)\n",
		workers, len(shopList))
	shards := make([]*web.CaptureShard, workers)
	var capWG sync.WaitGroup
	capErr := make(chan error, workers)
	for wi := 0; wi < workers; wi++ {
		shards[wi] = e.NewCaptureShard(wi)
		capWG.Add(1)
		go func(wi int) {
			defer capWG.Done()
			shard := make([]shops.Shop, 0, len(shopList)/workers+1)
			for i := wi; i < len(shopList); i += workers {
				shard = append(shard, shopList[i])
			}
			cs := shards[wi]
			_, gerr := transactions.GenerateForShard(tier, seed, asOf, shard, cat, hw, custIndex, nil, transactions.IDBase{}, func(tx transactions.Transaction) {
				if tx.CustomerID == nil || tx.OriginalTransactionID != nil {
					return
				}
				at := parseTxTime(tx.OccurredAt)
				for _, ln := range tx.Lines {
					if ln.ReleaseID == 0 && ln.HardwareID == 0 {
						continue
					}
					cs.CaptureSale(*tx.CustomerID, ln.ReleaseID, ln.HardwareID, at, ln.Condition, ln.LineTotal)
				}
			})
			if gerr != nil {
				select {
				case capErr <- fmt.Errorf("worker %d: %w", wi, gerr):
				default:
				}
			}
		}(wi)
	}
	capWG.Wait()
	close(capErr)
	for err := range capErr {
		return s, fmt.Errorf("transaction replay: %w", err)
	}
	e.MergeCaptureShards(shards)
	fmt.Fprintf(os.Stderr, "  web: %d accounts, %d reviewers; capturing purchases done\n",
		e.AccountCount(), e.ReviewerCount())

	// 4. Reviews / comments / votes IN MEMORY (comments+votes set the
	//    denormalised counts that must land on the review rows).
	// Only the reviews are held in memory (the comment/vote emitters iterate
	// them). Comments and votes are NOT held — that would be ~12 GB at 3T.
	// Instead the emitters run twice: a COUNT pass (mutates the reviews'
	// comment/helpful/funny counts, keeping nothing) and, after the reviews
	// are written, a STREAM pass that regenerates the identical rows
	// (deterministic) straight into a BatchedLoader. Peak memory is then
	// bounded by the reviews slice, not the far larger child tables.
	var reviews []web.ReviewRecord
	e.EmitReviews(1, func(r web.ReviewRecord) { reviews = append(reviews, r) })
	e.EmitComments(reviews, 1, func(web.CommentRecord) {}) // count → sets CommentCount
	e.EmitVotes(reviews, 1, func(web.VoteRecord) {})       // count → sets Helpful/FunnyCount (needs CommentCount above)

	// 5a. Reviews (FK parent) — written with the counts the passes set. Batched
	//     (one committed transaction per 50k rows) so the wide review-text log
	//     stays bounded on nge's small log drive.
	if s.Reviews, err = writeBatchedIfEmpty(ctx, w, "web", "reviews", "review_id", ReviewColumns,
		len(reviews), func(i int) []any { return ReviewRow(reviews[i]) }); err != nil {
		return s, fmt.Errorf("load web.reviews: %w", err)
	}
	progress("web.reviews", s.Reviews)

	// 5b. Comments — regenerated and streamed (skip if already loaded).
	if existing, err := w.MaxBigint(ctx, "web", "review_comments", "comment_id"); err != nil {
		return s, fmt.Errorf("check web.review_comments: %w", err)
	} else if existing == 0 {
		cl := NewBatchedLoader(ctx, w, "web", "review_comments", ReviewCommentColumns, 0)
		e.EmitComments(reviews, 1, func(c web.CommentRecord) {
			if cl.Err() == nil {
				_ = cl.Add(ReviewCommentRow(c))
			}
		})
		if err := cl.Flush(); err != nil {
			return s, fmt.Errorf("load web.review_comments: %w", err)
		}
		s.Comments = cl.Total()
	} else {
		s.Comments = existing
	}
	progress("web.review_comments", s.Comments)

	// 5c. Votes — regenerated and streamed.
	if existing, err := w.MaxBigint(ctx, "web", "review_votes", "vote_id"); err != nil {
		return s, fmt.Errorf("check web.review_votes: %w", err)
	} else if existing == 0 {
		vl := NewBatchedLoader(ctx, w, "web", "review_votes", ReviewVoteColumns, 0)
		e.EmitVotes(reviews, 1, func(v web.VoteRecord) {
			if vl.Err() == nil {
				_ = vl.Add(ReviewVoteRow(v))
			}
		})
		if err := vl.Flush(); err != nil {
			return s, fmt.Errorf("load web.review_votes: %w", err)
		}
		s.Votes = vl.Total()
	} else {
		s.Votes = existing
	}
	progress("web.review_votes", s.Votes)

	// 6. Clickstream — PARALLEL, streamed (too large to hold in memory).
	//    Days are sharded round-robin across `workers`, each with its own DB
	//    connection + disjoint id range. All-or-nothing resume: if page_views
	//    already has rows we skip (a mid-clickstream crash leaves disjoint id
	//    ranges MaxBigint can't reason about — TRUNCATE web.page_views and
	//    rerun to redo just this phase). Needs the DSN for per-worker writers.
	existing, err := w.MaxBigint(ctx, "web", "page_views", "page_view_id")
	if err != nil {
		return s, fmt.Errorf("check web.page_views: %w", err)
	}
	if existing > 0 {
		fmt.Fprintf(os.Stderr, "  web.page_views already has rows (max id %d) — skipping clickstream "+
			"(TRUNCATE web.page_views to regenerate)\n", existing)
		s.PageViews = existing
		progress("web.page_views", s.PageViews)
		return s, nil
	}
	if dsn == "" {
		return s, fmt.Errorf("parallel clickstream needs a DSN for per-worker writers")
	}
	fmt.Fprintf(os.Stderr, "  web: clickstream — %d workers, scale %g (day-sharded)\n", workers, clickScale)
	var pvWG sync.WaitGroup
	pvErr := make(chan error, workers)
	var totalPV int64
	for wi := 0; wi < workers; wi++ {
		pvWG.Add(1)
		go func(wi int) {
			defer pvWG.Done()
			ww, oerr := NewMSSQL(ctx, dsn)
			if oerr != nil {
				select {
				case pvErr <- fmt.Errorf("worker %d open db: %w", wi, oerr):
				default:
				}
				return
			}
			defer ww.Close()
			pv := NewBatchedLoader(ctx, ww, "web", "page_views", PageViewColumns, 0)
			base := int64(wi)*clickIDStride + 1
			var n int64
			e.EmitClickstreamShard(clickScale, wi, workers, base, base, func(v web.PageViewRecord) {
				if pv.Err() != nil {
					return
				}
				_ = pv.Add(PageViewRow(v))
				n++
			})
			if err := pv.Flush(); err != nil {
				select {
				case pvErr <- fmt.Errorf("worker %d flush: %w", wi, err):
				default:
				}
				return
			}
			atomic.AddInt64(&totalPV, n)
		}(wi)
	}
	pvWG.Wait()
	close(pvErr)
	for err := range pvErr {
		return s, fmt.Errorf("load web.page_views: %w", err)
	}
	s.PageViews = totalPV
	progress("web.page_views", s.PageViews)

	return s, nil
}

// ensureWebTables verifies the web schema exists, returning a clear error if
// not (the caller must apply the schema first — e.g. --init-schema).
func ensureWebTables(ctx context.Context, w Writer) error {
	if _, err := w.MaxBigint(ctx, "web", "accounts", "account_id"); err != nil {
		return fmt.Errorf("web.accounts not found — apply the schema first (--init-schema): %w", err)
	}
	return nil
}

func buildReleaseMeta(cat *catalog.Index) map[int64]web.ReleaseMeta {
	m := make(map[int64]web.ReleaseMeta, cat.Count())
	for _, r := range cat.All() {
		m[r.ReleaseID] = web.ReleaseMeta{
			NormTitle: strings.ToLower(strings.TrimSpace(r.Title)),
			Title:     r.Title,
			Platform:  r.Platform,
			Genre:     r.Genre,
			Publisher: r.Publisher,
			Developer: r.Developer,
		}
	}
	return m
}

func buildHardwareMeta(hw *hardware.Index) map[int64]web.HardwareMeta {
	m := make(map[int64]web.HardwareMeta, hw.Count())
	for _, h := range hw.All() {
		m[h.HardwareID] = web.HardwareMeta{
			ModelName:    h.ModelName,
			Platform:     h.Platform,
			Kind:         h.Kind,
			Manufacturer: h.Manufacturer,
		}
	}
	return m
}

// parseTxTime parses a transaction occurred_at string (ISO, T…Z) to time.
func parseTxTime(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
