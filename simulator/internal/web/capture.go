// Purchase capture + review emission. The load layer replays
// transactions.GenerateForShard with a callback into CaptureSale, which
// reconstructs each reviewer's real purchases (byte-identical to the loaded
// transactions — same seed, same streams) without rewriting a single
// transaction row. That is what makes reviews an ADDITIVE load: verified
// reviews reference genuine purchases, and the account→purchase link is a
// true join, not a fabrication. Design §4 / §8.
package web

import (
	"math/rand/v2"
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

// reviewerState holds one reviewer's reservoir + total purchase count (the
// whale signal) + a private RNG for reservoir replacement.
type reviewerState struct {
	reservoir []Purchase
	count     int
	rng       *rand.Rand
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
			e.reviewers[cid] = &reviewerState{rng: rng.Derive(e.seed, "web/reservoir/"+itoa(cid))}
		}
	}
}

// CaptureSale records one forward sale line for its customer, IF that
// customer is a reviewer. Called from the replay callback per qualifying
// line (forward sales only — no refunds; exactly one of releaseID/hardwareID
// set). Reservoir sampling (algorithm R) keeps a bounded, deterministic
// sample when a reviewer has more than reservoirK purchases.
func (e *Emitter) CaptureSale(customerID, releaseID, hardwareID int64, at time.Time, condition string, price float64) {
	st := e.reviewers[customerID]
	if st == nil {
		return
	}
	st.count++
	p := Purchase{ReleaseID: releaseID, HardwareID: hardwareID, At: at, Condition: condition, Price: price}
	if len(st.reservoir) < reservoirK {
		st.reservoir = append(st.reservoir, p)
		return
	}
	if j := st.rng.IntN(st.count); j < reservoirK {
		st.reservoir[j] = p
	}
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
// not change which sales exist. The reservoir CONTENTS differ from the serial
// pass (a customer's sales can span shards), but that only changes which real
// purchases a reviewer samples from — never their reality — and the result is
// identical on every run (fixed worker order + per-customer merge stream).

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
// a reviewer. Mirrors Emitter.CaptureSale but into shard-local state with a
// per-(worker,customer) reservoir stream.
func (cs *CaptureShard) CaptureSale(customerID, releaseID, hardwareID int64, at time.Time, condition string, price float64) {
	if cs.e.reviewers[customerID] == nil {
		return // not a reviewer — concurrent read of a frozen map is safe
	}
	st := cs.local[customerID]
	if st == nil {
		st = &reviewerState{rng: rng.Derive(cs.e.seed, "web/capshard/"+itoa(int64(cs.worker))+"/"+itoa(customerID))}
		cs.local[customerID] = st
	}
	st.count++
	p := Purchase{ReleaseID: releaseID, HardwareID: hardwareID, At: at, Condition: condition, Price: price}
	if len(st.reservoir) < reservoirK {
		st.reservoir = append(st.reservoir, p)
		return
	}
	if j := st.rng.IntN(st.count); j < reservoirK {
		st.reservoir[j] = p
	}
}

// MergeCaptureShards folds worker shards into e.reviewers deterministically:
// per reviewer, concatenate the shard reservoirs in worker order, sum the
// purchase counts (the whale signal), and down-sample to reservoirK with a
// per-customer stream. Call once after all workers finish.
func (e *Emitter) MergeCaptureShards(shards []*CaptureShard) {
	for cid, st := range e.reviewers {
		var pool []Purchase
		total := 0
		for _, cs := range shards { // fixed worker order 0..N-1
			if lst := cs.local[cid]; lst != nil {
				pool = append(pool, lst.reservoir...)
				total += lst.count
			}
		}
		st.count = total
		if len(pool) <= reservoirK {
			st.reservoir = pool
			continue
		}
		mr := rng.Derive(e.seed, "web/capmerge/"+itoa(cid))
		res := make([]Purchase, 0, reservoirK)
		for i, p := range pool {
			if i < reservoirK {
				res = append(res, p)
			} else if j := mr.IntN(i + 1); j < reservoirK {
				res[j] = p
			}
		}
		st.reservoir = res
	}
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
