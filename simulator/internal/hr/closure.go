// V1.15.0 — closure post-pass.
//
// applyClosure sets EndedAt + TerminationReason on each Person's spell
// based on whether they're retail (anchored to a shop's closed_date)
// or HQ/senior (anchored to a corporate-event schedule).
//
// The post-pass is invoked per-person from the wrapping emit callback
// in hr.Generate; this keeps existing emitRetailStaff / emitHQStaff /
// emitSeniorLeaders logic untouched.

package hr

import (
	"fmt"
	"math/rand/v2"
	"time"

	"emporium/internal/policy"
	"emporium/internal/rng"
	"emporium/internal/shops"
)

// applyClosure mutates p.Spell to set EndedAt + TerminationReason per
// the V1.15.0 wind-down narrative. Called once per emitted Person.
func applyClosure(p *Person, shopByID map[int64]shops.Shop, seed uint64) {
	if p.Spell.EndedAt != nil {
		return
	}
	started, err := time.Parse("2006-01-02", p.Spell.StartedAt)
	if err != nil {
		return
	}

	r := rng.Derive(seed, fmt.Sprintf("hr/term/%d", p.PersonID))

	var ended time.Time
	var reason string
	if p.Spell.HomeShopID != nil {
		ended, reason = retailTermination(started, *p.Spell.HomeShopID, shopByID, r)
	} else {
		ended, reason = hqTermination(started, p.Spell.PayGradeID, r)
	}
	if ended.IsZero() || reason == "" {
		return
	}
	if ended.Before(started) {
		ended = started
	}
	dateStr := ended.Format("2006-01-02")
	reasonCopy := reason
	p.Spell.EndedAt = &dateStr
	p.Spell.TerminationReason = &reasonCopy
}

// retailTermination decides how a retail employee leaves NGE. Most
// retail staff turn over before their shop closes (retail turnover is
// famously ~40-50%/yr); the remainder stay until the shop closes and
// get the shop's closure_reason.
func retailTermination(
	started time.Time,
	homeShopID int64,
	shopByID map[int64]shops.Shop,
	r *rand.Rand,
) (time.Time, string) {
	shop, ok := shopByID[homeShopID]
	if !ok || shop.ClosedDate == nil {
		return time.Time{}, ""
	}
	shopClose, err := time.Parse("2006-01-02", *shop.ClosedDate)
	if err != nil {
		return time.Time{}, ""
	}

	availableYears := shopClose.Sub(started).Hours() / (24 * 365.25)
	if availableYears <= 0 {
		return shopClose, shop.ClosureReason
	}

	// Probability of leaving before shop closes scales with the
	// available window. A staff member at a shop open for 25 more
	// years almost certainly turns over; one with only 6 months left
	// is more likely a "stayed until the end" record.
	earlyExitProb := 0.85 * (1.0 - 1.0/(1.0+availableYears*0.5))
	if r.Float64() > earlyExitProb {
		return shopClose, shop.ClosureReason
	}

	// Early attrition. ExpFloat64 has mean 1; scale to ~2-year mean
	// tenure, capped at the available window and 10 years.
	tenureYears := r.ExpFloat64() * 2.0
	if tenureYears > availableYears {
		tenureYears = availableYears
	}
	if tenureYears > 10 {
		tenureYears = 10
	}
	tenureDays := int(tenureYears * 365.25)
	if tenureDays < 1 {
		tenureDays = 1
	}
	ended := started.AddDate(0, 0, tenureDays)
	if ended.After(shopClose) {
		ended = shopClose
	}
	return ended, "resignation"
}

// hqTermination decides how an HQ / senior-leadership employee leaves
// NGE. Bucketed corporate events: 2014 restructuring (~30% redundancies
// from the 155-store closure programme), 2015 layoff (~50% remaining
// HQ), 2016 Chapter 11 mass redundancy, plus the wind-down crew that
// stays until 2016-09-30 liquidation.
func hqTermination(
	started time.Time,
	payGradeID int,
	r *rand.Rand,
) (time.Time, string) {
	// Senior leadership (pay grades G8/G9/G10): mostly retained
	// through Chapter 11 (paid retention bonuses), some resign early.
	if payGradeID >= 8 {
		if r.Float64() < 0.25 {
			ended := time.Date(2015, time.Month(1+int(r.Float64()*12)), 15, 0, 0, 0, 0, time.UTC)
			if ended.After(started) {
				return ended, "resignation"
			}
		}
		return policy.BusinessClosureDate, "liquidation_2016"
	}

	// HQ rank-and-file: bucket into termination cohorts.
	roll := r.Float64()
	switch {
	case roll < 0.12: // ~12% normal attrition 2007-2014
		year := 2007 + int(r.Float64()*8)
		month := time.Month(1 + int(r.Float64()*12))
		ended := time.Date(year, month, 15, 0, 0, 0, 0, time.UTC)
		if ended.Before(started) {
			ended = started.AddDate(2, 0, 0)
		}
		return ended, "resignation"
	case roll < 0.25: // 2014 restructuring redundancy
		return ensureAfter(time.Date(2014, time.March, 15, 0, 0, 0, 0, time.UTC), started), "restructuring_2014"
	case roll < 0.55: // 2015 corporate layoffs
		return ensureAfter(time.Date(2015, time.January, 15, 0, 0, 0, 0, time.UTC), started), "restructuring_2015"
	case roll < 0.96: // 2016 Chapter 11 mass redundancy
		return ensureAfter(time.Date(2016, time.February, 8, 0, 0, 0, 0, time.UTC), started), "chapter11_2016"
	default: // ~4% wind-down crew retained to liquidation
		return policy.BusinessClosureDate, "liquidation_2016"
	}
}

func ensureAfter(target, started time.Time) time.Time {
	if target.Before(started) {
		return started
	}
	return target
}

// trimOpsToTermination drops shift and payroll-line rows dated after
// the spell's termination (V1.17.0 — audit finding F7).
//
// Why this exists: emitRetailStaff generates operations rows with the
// shop's closure date as the working window, and applyClosure only
// AFTERWARDS backdates most spells' EndedAt (retail turnover means
// most staff leave years before their shop closes). Pre-V1.17 the
// leftover rows shipped: at the 3T tier, 26.6M of 47.9M shifts (55%)
// and 18.3M of 24.1M payroll lines (76%) post-dated the employee's
// own termination — three decades of phantom rota.
//
// Shifts: kept when the shift STARTS on or before the termination
// date (a final shift ON the last day is legitimate). Payroll lines:
// kept when their run's period STARTS on or before termination — the
// final, partial pay period still pays out, which is how real payroll
// behaves.
func trimOpsToTermination(p *Person, runStart map[int64]time.Time) {
	if p.Spell.EndedAt == nil {
		return
	}
	end, err := time.Parse("2006-01-02", *p.Spell.EndedAt)
	if err != nil {
		return
	}
	endOfDay := end.AddDate(0, 0, 1)

	if len(p.Shifts) > 0 {
		kept := p.Shifts[:0]
		for _, s := range p.Shifts {
			t, perr := time.Parse("2006-01-02T15:04:05.000Z", s.ShiftStart)
			if perr != nil || t.Before(endOfDay) {
				kept = append(kept, s)
			}
		}
		p.Shifts = kept
	}

	if len(p.PayrollLines) > 0 && len(runStart) > 0 {
		kept := p.PayrollLines[:0]
		for _, pl := range p.PayrollLines {
			st, ok := runStart[pl.PayrollRunID]
			if !ok || !st.After(end) {
				kept = append(kept, pl)
			}
		}
		p.PayrollLines = kept
	}
}
