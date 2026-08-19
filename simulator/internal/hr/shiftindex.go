// V1.18.0 — in-memory shift rota index.
//
// Built once during the HR load phase (before transactions generate)
// so the transactions package can answer "which retail spell was on
// duty at shop S at instant T?" without re-reading the database. The
// transactions side consumes this through its StaffPicker interface —
// hr stays import-free of transactions, mirroring the CustomerPicker
// pattern from V1.14.0.
//
// Memory: 24 bytes per retained shift span. At the 3T tier
// (~21M post-trim shifts) that is ~0.5 GB plus map overhead —
// budgeted in PROJECT_NOTES §10.37.
//
// Concurrency contract: AddPerson is single-goroutine (the HR
// generate loop); after Finalize() the index is READ-ONLY and safe to
// share across parallel transaction workers. PickOnDuty performs no
// lazy mutation.

package hr

import (
	"math/rand/v2"
	"sort"
	"time"
)

// shiftSpan is one rostered shift, times in unix seconds.
type shiftSpan struct {
	start, end time.Time
	spellID    int64
}

// ShiftIndex answers on-duty lookups per shop.
type ShiftIndex struct {
	byShop    map[int64][]shiftSpan
	finalized bool
}

// NewShiftIndex returns an empty index.
func NewShiftIndex() *ShiftIndex {
	return &ShiftIndex{byShop: make(map[int64][]shiftSpan)}
}

// shiftLayout matches operations.go's formatting of ShiftStart/End.
const shiftLayout = "2006-01-02T15:04:05.000Z"

// maxShiftSpanHours bounds the walk-back in PickOnDuty. Shifts are
// 6-9 hours (operations.go); 12 gives slack without scanning far.
const maxShiftSpanHours = 12

// AddPerson appends a person's (post-trim) shifts. Call BEFORE any
// resume-skip logic — the index must be complete even when the DB
// rows themselves are skipped on a resumed load.
func (ix *ShiftIndex) AddPerson(p Person) {
	if ix == nil || len(p.Shifts) == 0 {
		return
	}
	spellID := p.Spell.SpellID
	for _, s := range p.Shifts {
		start, err := time.Parse(shiftLayout, s.ShiftStart)
		if err != nil {
			continue
		}
		end, err := time.Parse(shiftLayout, s.ShiftEnd)
		if err != nil {
			continue
		}
		ix.byShop[s.ShopID] = append(ix.byShop[s.ShopID], shiftSpan{
			start: start, end: end, spellID: spellID,
		})
	}
}

// Finalize sorts each shop's spans by start time. Must be called once,
// after the last AddPerson and before the first PickOnDuty.
func (ix *ShiftIndex) Finalize() {
	if ix == nil {
		return
	}
	for shop := range ix.byShop {
		spans := ix.byShop[shop]
		sort.Slice(spans, func(i, j int) bool {
			if !spans[i].start.Equal(spans[j].start) {
				return spans[i].start.Before(spans[j].start)
			}
			return spans[i].spellID < spans[j].spellID
		})
	}
	ix.finalized = true
}

// SpanCount reports the total retained spans (smoke/diagnostics).
func (ix *ShiftIndex) SpanCount() int {
	if ix == nil {
		return 0
	}
	n := 0
	for _, s := range ix.byShop {
		n += len(s)
	}
	return n
}

// PickOnDuty returns the spell_id of a uniformly-chosen staff member
// whose shift covers `at` at the given shop (start <= at < end), or
// ok=false when nobody is rostered. RNG is consumed ONLY when at
// least one candidate exists — callers' streams stay self-contained
// across data changes elsewhere.
func (ix *ShiftIndex) PickOnDuty(shopID int64, at time.Time, r *rand.Rand) (int64, bool) {
	if ix == nil {
		return 0, false
	}
	spans := ix.byShop[shopID]
	if len(spans) == 0 {
		return 0, false
	}
	// First span starting AFTER `at`; candidates start at or before
	// `at`, and no shift is longer than maxShiftSpanHours, so only the
	// window (at - maxShiftSpanHours, at] needs scanning backwards.
	hi := sort.Search(len(spans), func(i int) bool {
		return spans[i].start.After(at)
	})
	floor := at.Add(-maxShiftSpanHours * time.Hour)
	var candidates []int64
	for i := hi - 1; i >= 0; i-- {
		if spans[i].start.Before(floor) {
			break
		}
		if !spans[i].start.After(at) && spans[i].end.After(at) {
			candidates = append(candidates, spans[i].spellID)
		}
	}
	if len(candidates) == 0 {
		return 0, false
	}
	return candidates[r.IntN(len(candidates))], true
}
