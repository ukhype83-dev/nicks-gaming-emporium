package web

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"emporium/internal/catalog"
	"emporium/internal/customers"
	"emporium/internal/geography"
	"emporium/internal/hardware"
	"emporium/internal/shops"
	"emporium/internal/transactions"
)

// preCaptureWorld builds accounts + reviewers + custIndex + shops, stopping
// just before purchase capture — shared setup for the parallel-capture tests.
func preCaptureWorld(t *testing.T, seed uint64) (*Emitter, *customers.Index, []shops.Shop, *catalog.Index, *hardware.Index) {
	t.Helper()
	asOf := time.Date(2025, 4, 23, 0, 0, 0, 0, time.UTC)
	postals, err := geography.Load(postalPath)
	if err != nil {
		t.Fatalf("postals: %v", err)
	}
	cat, err := catalog.Load(catalogPath)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	hw, err := hardware.Load(hardwarePath)
	if err != nil {
		t.Fatalf("hardware: %v", err)
	}
	e := NewEmitter(loadBanksT(t), seed, asOf, 1)
	_, custIndex, err := customers.Generate("3g", seed, asOf, postals, func(c customers.Customer) { e.AccountFor(c) })
	if err != nil {
		t.Fatalf("customers: %v", err)
	}
	e.MarkReviewers()
	e.SetCatalogs(releaseMetaFrom(cat), hardwareMetaFrom(hw))
	shopList, err := shops.Generate("3g", seed, asOf, postals)
	if err != nil {
		t.Fatalf("shops: %v", err)
	}
	return e, custIndex, shopList, cat, hw
}

// runShardedCapture replays the transaction stream across `workers` round-robin
// shop shards (in goroutines, so -race exercises concurrency safety) and merges.
func runShardedCapture(e *Emitter, custIndex *customers.Index, shopList []shops.Shop, cat *catalog.Index, hw *hardware.Index, workers int, seed uint64) {
	asOf := time.Date(2025, 4, 23, 0, 0, 0, 0, time.UTC)
	shards := make([]*CaptureShard, workers)
	var wg sync.WaitGroup
	for wi := 0; wi < workers; wi++ {
		shards[wi] = e.NewCaptureShard(wi)
		wg.Add(1)
		go func(wi int) {
			defer wg.Done()
			var shard []shops.Shop
			for i := wi; i < len(shopList); i += workers {
				shard = append(shard, shopList[i])
			}
			cs := shards[wi]
			_, _ = transactions.GenerateForShard("3g", seed, asOf, shard, cat, hw, custIndex, nil, transactions.IDBase{}, func(tx transactions.Transaction) {
				if tx.CustomerID == nil || tx.OriginalTransactionID != nil {
					return
				}
				at := parseCustDate(tx.OccurredAt)
				for _, ln := range tx.Lines {
					if ln.ReleaseID == 0 && ln.HardwareID == 0 {
						continue
					}
					cs.CaptureSale(*tx.CustomerID, ln.ReleaseID, ln.HardwareID, at, ln.Condition, ln.LineTotal)
				}
			})
		}(wi)
	}
	wg.Wait()
	e.MergeCaptureShards(shards)
}

// reviewerCounts snapshots each reviewer's total captured purchase count (the
// whale signal). This is shard-invariant — it is just the number of real sales
// for that customer — so it is the sharp coherence check.
func reviewerCounts(e *Emitter) map[int64]int {
	m := make(map[int64]int, len(e.reviewers))
	for cid, st := range e.reviewers {
		if st.count > 0 {
			m[cid] = st.count
		}
	}
	return m
}

// TestCaptureShardsMatchSerialCounts proves the parallel replay sees exactly the
// same sales as the serial one: every reviewer's total purchase count is
// identical no matter how many shards partition the shops. (Counts are the
// coherence check here; TestCaptureReservoirContentsWorkerInvariant additionally
// proves the reservoir CONTENTS are worker-invariant.)
func TestCaptureShardsMatchSerialCounts(t *testing.T) {
	// Serial baseline.
	es, custIndex, shopList, cat, hw := preCaptureWorld(t, 42)
	asOf := time.Date(2025, 4, 23, 0, 0, 0, 0, time.UTC)
	if _, err := transactionsGenerate(42, asOf, shopList, cat, hw, custIndex, es); err != nil {
		t.Fatalf("serial capture: %v", err)
	}
	serial := reviewerCounts(es)

	for _, workers := range []int{2, 3, 5} {
		ep, ci, sl, c, h := preCaptureWorld(t, 42)
		runShardedCapture(ep, ci, sl, c, h, workers, 42)
		got := reviewerCounts(ep)
		if len(got) != len(serial) {
			t.Fatalf("workers=%d: %d reviewers-with-purchases vs serial %d", workers, len(got), len(serial))
		}
		for cid, want := range serial {
			if got[cid] != want {
				t.Fatalf("workers=%d: reviewer %d count %d vs serial %d", workers, cid, got[cid], want)
			}
		}
	}
}

// TestCaptureShardDeterminism: the same worker count reproduces byte-identical
// reservoirs (fixed worker order + per-customer merge stream).
func TestCaptureShardDeterminism(t *testing.T) {
	sig := func() map[int64]string {
		e, ci, sl, c, h := preCaptureWorld(t, 42)
		runShardedCapture(e, ci, sl, c, h, 4, 42)
		m := make(map[int64]string, len(e.reviewers))
		for cid, st := range e.reviewers {
			s := fmt.Sprintf("c%d|", st.count)
			for _, p := range st.reservoir {
				s += fmt.Sprintf("%d:%d:%d;", p.ReleaseID, p.HardwareID, p.At.Unix())
			}
			m[cid] = s
		}
		return m
	}
	a, b := sig(), sig()
	if len(a) != len(b) {
		t.Fatalf("reviewer count differs across runs: %d vs %d", len(a), len(b))
	}
	for cid, sa := range a {
		if b[cid] != sa {
			t.Fatalf("reviewer %d reservoir non-deterministic:\n a=%s\n b=%s", cid, sa, b[cid])
		}
	}
}

// TestCaptureReservoirContentsWorkerInvariant guards the determinism fix: the
// bottom-k-by-hash reservoir CONTENTS (not merely the counts) must be identical
// for every worker count, serial included. Before the fix an algorithm-R
// reservoir consumed its RNG in capture order, so the sample — and thus the
// review/comment/vote counts — silently tracked the machine's core count.
func TestCaptureReservoirContentsWorkerInvariant(t *testing.T) {
	asOf := time.Date(2025, 4, 23, 0, 0, 0, 0, time.UTC)

	// Per-reviewer signature: count + the SORTED reservoir. Sorting mirrors
	// EmitReviews (which sorts before consuming), so we compare the exact set
	// the emitter sees, independent of merge/insertion order.
	sig := func(e *Emitter) map[int64]string {
		m := make(map[int64]string, len(e.reviewers))
		for cid, st := range e.reviewers {
			sortPurchases(st.reservoir)
			s := fmt.Sprintf("c%d|", st.count)
			for _, p := range st.reservoir {
				s += fmt.Sprintf("%d:%d:%d:%s:%.4f;", p.ReleaseID, p.HardwareID, p.At.Unix(), p.Condition, p.Price)
			}
			m[cid] = s
		}
		return m
	}

	// Serial baseline (Emitter.CaptureSale over every shop).
	es, ci0, sl0, c0, h0 := preCaptureWorld(t, 42)
	if _, err := transactionsGenerate(42, asOf, sl0, c0, h0, ci0, es); err != nil {
		t.Fatalf("serial capture: %v", err)
	}
	base := sig(es)

	for _, workers := range []int{1, 2, 3, 5, 8} {
		ep, ci, sl, c, h := preCaptureWorld(t, 42)
		runShardedCapture(ep, ci, sl, c, h, workers, 42)
		got := sig(ep)
		if len(got) != len(base) {
			t.Fatalf("workers=%d: %d reviewers vs serial %d", workers, len(got), len(base))
		}
		for cid, want := range base {
			if got[cid] != want {
				t.Fatalf("workers=%d: reviewer %d reservoir differs from serial:\n serial=%s\n got   =%s",
					workers, cid, want, got[cid])
			}
		}
	}
}

// TestClickstreamShardEquivalence: day-sharding partitions traffic but never
// changes it — the union of N shards has the identical content multiset (every
// field except the disjoint id counters) as the single-shard stream.
func TestClickstreamShardEquivalence(t *testing.T) {
	_, e := buildWorld(t, 42)
	type key struct {
		at              int64
		url, country, ua, ref string
		status, bytes   int
		acct            int64
	}
	tally := func(collect func(func(PageViewRecord))) map[key]int {
		m := map[key]int{}
		collect(func(v PageViewRecord) {
			m[key{v.OccurredAt.Unix(), v.URLPath, v.ClientCountry, v.UserAgentFamily, v.ReferrerDomain, v.HTTPStatus, v.BytesSent, v.AccountID}]++
		})
		return m
	}
	single := tally(func(emit func(PageViewRecord)) { e.EmitClickstream(0.00003, 1, 1, emit) })
	const N = 4
	const stride int64 = 1_000_000_000_000 // matches dbwriter.clickIDStride
	sharded := tally(func(emit func(PageViewRecord)) {
		for wi := 0; wi < N; wi++ {
			base := int64(wi)*stride + 1
			e.EmitClickstreamShard(0.00003, wi, N, base, base, emit)
		}
	})
	if len(single) != len(sharded) {
		t.Fatalf("distinct content rows differ: single %d vs %d-shard %d", len(single), N, len(sharded))
	}
	for k, c := range single {
		if sharded[k] != c {
			t.Fatalf("content multiplicity differs (single %d vs sharded %d) for %+v", c, sharded[k], k)
		}
	}
}
