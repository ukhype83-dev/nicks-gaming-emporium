// Purchase capture + review emission. The load layer replays
// transactions.GenerateForShard with a callback into CaptureSale, which
// reconstructs each reviewer's real purchases (byte-identical to the loaded
// transactions — same seed, same streams) without rewriting a single
// transaction row. That is what makes reviews an ADDITIVE load: verified
// reviews reference genuine purchases, and the account→purchase link is a
// true join, not a fabrication. Design §4 / §8.
package web

import (
	"math"
	"sort"
	"time"

	"emporium/internal/rng"
)

const (
	// reservoirK caps captured purchases per reviewer — reviews sample from
	// this pool, so it need only exceed a heavy reviewer's review count.
	reservoirK = 12
	// baseReviewerRate: P(an account holder is a reviewer). Calibration
	// history: 0.18 → ~3.8M @3T (English only); 0.38 → ~8M English-only.
	// With the 9 non-English banks live (~29% more reviewers who used to be
	// skipped now write), 0.38 measures ~114K @30g → **~11.4M @3T, which the
	// user ACCEPTED 2026-07-19** as the all-language total. Do NOT trim back
	// to 8M — 11.4M is the agreed figure.
	baseReviewerRate = 0.38
)

// ReleaseMeta / HardwareMeta are the review-context fields the load layer
// pre-resolves from the catalogs (catalog has no ByID; we build the map
// once from cat.All()). NormTitle is the lowercased title the reception
// matcher keys on.
type ReleaseMeta struct {
	NormTitle string
	Title     string // display form (region-denorm tail trimmed)
	Platform  string
	Genre     string
	Publisher string
	Developer string
	// V1.27 review-immersion plumbing, pre-resolved in buildReleaseMeta so
	// internal/web stays policy-free: ReleaseYear (0 if the date is unknown),
	// Media (the raw policy.CanonicalMedia string, "" if unknown — classified
	// into cartridge/disc/… by the review engine), and OtherPlatforms (the
	// sibling platforms the same title shipped on, sorted, excluding this
	// release's own platform; nil = single-platform title).
	ReleaseYear    int
	Media          string
	OtherPlatforms []string
}

type HardwareMeta struct {
	ModelName    string
	Platform     string
	Kind         string
	Manufacturer string
}

// Purchase is a trimmed snapshot of one forward sale line — enough to write
// a verified review about it.
type Purchase struct {
	ReleaseID  int64
	HardwareID int64
	At         time.Time
	Condition  string
	Price      float64
}

// reviewerState holds one reviewer's reservoir (a deterministic bottom-k
// sample of their purchases) + total purchase count (the whale signal). pri
// holds the selection-hash priority of each reservoir slot, kept parallel to
// reservoir so the accumulation hot path never rehashes.
type reviewerState struct {
	reservoir []Purchase
	pri       []uint64
	count     int
}

// SetCatalogs installs the pre-resolved review-context lookups. Call once,
// before the capture replay.
func (e *Emitter) SetCatalogs(rel map[int64]ReleaseMeta, hw map[int64]HardwareMeta) {
	e.releaseMeta = rel
	e.hardwareMeta = hw
}

// MarkReviewers decides, deterministically per customer, which account
// holders are reviewers and pre-creates their (empty) reservoirs. Runs after
// account emission; the capture replay then fills the reservoirs. Iterating
// the account map in any order is safe — the decision is per-customer.
func (e *Emitter) MarkReviewers() {
	e.reviewers = make(map[int64]*reviewerState, e.AccountCount()/5)
	for cid := range e.accountByCustomer {
		if rng.Derive(e.seed, "web/reviewer/"+itoa(cid)).Float64() < baseReviewerRate {
			e.reviewers[cid] = &reviewerState{}
		}
	}
}

// CaptureSale records one forward sale line for its customer, IF that
// customer is a reviewer. Called from the replay callback per qualifying
// line (forward sales only — no refunds; exactly one of releaseID/hardwareID
// set). Keeps a bounded bottom-k-by-hash sample (see reservoirConsider) that
// is independent of capture order.
func (e *Emitter) CaptureSale(customerID, releaseID, hardwareID int64, at time.Time, condition string, price float64) {
	st := e.reviewers[customerID]
	if st == nil {
		return
	}
	st.count++
	e.reservoirConsider(st, customerID, Purchase{ReleaseID: releaseID, HardwareID: hardwareID, At: at, Condition: condition, Price: price})
}

// ReviewerCount is the number of reviewers marked.
func (e *Emitter) ReviewerCount() int { return len(e.reviewers) }

// --- Parallel capture (design §8) -------------------------------------------
//
// The serial CaptureSale above is fine at small tiers, but the 3T replay of
// ~1.36 B transactions is CPU-bound and single-threaded. To parallelise it we
// shard shops round-robin across N workers; each worker replays its shard into
// a private CaptureShard (no shared mutable state → lock-free), then
// MergeCaptureShards folds the shards into e.reviewers deterministically.
//
// Coherence is preserved: every captured purchase is a real replayed sale, and
// the per-shop RNG is independent of shard assignment, so the shard split does
// not change which sales exist. The reservoir is a bottom-k-by-hash sample
// (reservoirConsider), a commutative reduction: each shard keeps its local
// bottom-k, the merge keeps the global bottom-k, and any globally-bottom-k
// purchase is bottom-k within its own shard — so the reservoir CONTENTS are
// identical for ANY worker count, including a serial (1-shard) run. This
// replaced an algorithm-R reservoir whose RNG stream was consumed in capture
// order, which silently made the sample — and thus the review/comment/vote
// counts — depend on the shard count (stable only for a fixed N).

// CaptureShard accumulates one worker's slice of the transaction replay.
type CaptureShard struct {
	e      *Emitter
	worker int
	local  map[int64]*reviewerState
}

// NewCaptureShard returns a capture sink for worker w. Safe to run concurrently
// with sibling shards: it only READS e.reviewers (the reviewer set, frozen
// after MarkReviewers) and writes its own local map.
func (e *Emitter) NewCaptureShard(worker int) *CaptureShard {
	return &CaptureShard{e: e, worker: worker, local: make(map[int64]*reviewerState)}
}

// CaptureSale records one forward sale line into this shard, if the customer is
// a reviewer. Mirrors Emitter.CaptureSale into shard-local state; the bottom-k
// sample is order-independent, so the shard's slice order does not matter.
func (cs *CaptureShard) CaptureSale(customerID, releaseID, hardwareID int64, at time.Time, condition string, price float64) {
	if cs.e.reviewers[customerID] == nil {
		return // not a reviewer — concurrent read of a frozen map is safe
	}
	st := cs.local[customerID]
	if st == nil {
		st = &reviewerState{}
		cs.local[customerID] = st
	}
	st.count++
	cs.e.reservoirConsider(st, customerID, Purchase{ReleaseID: releaseID, HardwareID: hardwareID, At: at, Condition: condition, Price: price})
}

// MergeCaptureShards folds worker shards into e.reviewers. Per reviewer it sums
// the purchase counts (the whale signal — partition-independent) and re-runs the
// bottom-k-by-hash selection over the union of the shards' local bottom-k
// samples. Since each shard already kept its local bottom-k, that union holds
// every globally-bottom-k purchase, so the result equals the global bottom-k —
// identical for any worker count. Call once after all workers finish.
func (e *Emitter) MergeCaptureShards(shards []*CaptureShard) {
	for cid, st := range e.reviewers {
		st.reservoir = st.reservoir[:0]
		st.pri = st.pri[:0]
		total := 0
		for _, cs := range shards {
			lst := cs.local[cid]
			if lst == nil {
				continue
			}
			total += lst.count
			for _, p := range lst.reservoir {
				e.reservoirConsider(st, cid, p)
			}
		}
		st.count = total
	}
}

// purchasePriority is a deterministic, uniformly-distributed 64-bit selection
// key for a captured purchase, keyed by the root seed and the reviewer. Its
// value does not depend on capture order or shard partitioning, so keeping the
// smallest-priority purchases yields a sample that is identical for any worker
// count. The leading domain tag isolates this hash stream from other Mix64
// users.
func (e *Emitter) purchasePriority(customerID int64, p Purchase) uint64 {
	return rng.Mix64(e.seed,
		0x77_65_62_72_65_73_70_6b, // "webrespk"
		uint64(customerID), uint64(p.ReleaseID), uint64(p.HardwareID),
		uint64(p.At.Unix()), rng.FoldString(p.Condition), math.Float64bits(p.Price))
}

// reservoirConsider keeps st.reservoir as the reservoirK purchases with the
// smallest selection priority (a bottom-k sketch). st.pri is maintained
// parallel to st.reservoir so the hot path rehashes only the candidate. The
// total order is (priority asc, then a stable field tuple) so a hash tie
// between two distinct purchases still resolves the same way on every run.
func (e *Emitter) reservoirConsider(st *reviewerState, customerID int64, p Purchase) {
	pr := e.purchasePriority(customerID, p)
	if len(st.reservoir) < reservoirK {
		st.reservoir = append(st.reservoir, p)
		st.pri = append(st.pri, pr)
		return
	}
	worst := 0 // slot with the greatest priority (least likely to belong)
	for i := 1; i < len(st.pri); i++ {
		if priGreater(st.pri[i], st.reservoir[i], st.pri[worst], st.reservoir[worst]) {
			worst = i
		}
	}
	if priGreater(st.pri[worst], st.reservoir[worst], pr, p) { // held worst > candidate → swap in
		st.reservoir[worst] = p
		st.pri[worst] = pr
	}
}

// priGreater reports whether (aPri,a) ranks after (bPri,b) in the total order
// (priority ascending, then a stable field tuple to break hash ties).
func priGreater(aPri uint64, a Purchase, bPri uint64, b Purchase) bool {
	if aPri != bPri {
		return aPri > bPri
	}
	if !a.At.Equal(b.At) {
		return a.At.After(b.At)
	}
	if a.ReleaseID != b.ReleaseID {
		return a.ReleaseID > b.ReleaseID
	}
	if a.HardwareID != b.HardwareID {
		return a.HardwareID > b.HardwareID
	}
	if a.Condition != b.Condition {
		return a.Condition > b.Condition
	}
	return a.Price > b.Price
}

// sortPurchases orders a reviewer's reservoir into a canonical (chronological,
// then stable field tuple) sequence, so review emission — which consumes a
// per-reviewer RNG stream in slice order — is independent of how the reservoir
// was accumulated or merged.
func sortPurchases(ps []Purchase) {
	sort.Slice(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		if a.ReleaseID != b.ReleaseID {
			return a.ReleaseID < b.ReleaseID
		}
		if a.HardwareID != b.HardwareID {
			return a.HardwareID < b.HardwareID
		}
		if a.Condition != b.Condition {
			return a.Condition < b.Condition
		}
		return a.Price < b.Price
	})
}

// EmitReviews walks the reviewers in deterministic (ascending customer_id)
// order and streams every review through emit. IDs start at reviewIDBase.
// Returns the next free review_id. Only English-language reviewers produce
// rows in tranche 1 (design defers non-English banks); the language mix is
// still assigned honestly, non-English reviewers are simply skipped until
// their banks land.
func (e *Emitter) EmitReviews(reviewIDBase int64, emit func(ReviewRecord)) int64 {
	if reviewIDBase == 0 {
		reviewIDBase = 1
	}
	reviewID := reviewIDBase

	cids := make([]int64, 0, len(e.reviewers))
	for cid := range e.reviewers {
		cids = append(cids, cid)
	}
	sort.Slice(cids, func(i, j int) bool { return cids[i] < cids[j] })

	for _, cid := range cids {
		st := e.reviewers[cid]
		sortPurchases(st.reservoir) // canonical order → emission independent of capture/merge order
		acct := e.acct(cid)
		rr := rng.Derive(e.seed, "web/reviews/"+itoa(cid))

		lang := languageFor(acct.country, rr)
		if !e.Banks.HasLanguage(lang) {
			continue // non-English reviewer — defer until the bank exists
		}
		arch := e.pickArchetype(cid, st.count)

		for i := range st.reservoir {
			p := st.reservoir[i]
			if rr.Float64() >= perPurchaseReviewProb(arch.ID) {
				continue
			}
			rec, ok := e.buildReview(rr, reviewID, acct, arch, lang, p)
			if !ok {
				continue
			}
			emit(rec)
			reviewID++
		}
		// A few unverified (browsed-not-bought) reviews.
		for n := unverifiedCount(rr, arch.ID); n > 0; n-- {
			rec, ok := e.buildUnverifiedReview(rr, reviewID, acct, arch, lang)
			if !ok {
				continue
			}
			emit(rec)
			reviewID++
		}
	}
	return reviewID
}
