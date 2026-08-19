package shops

import (
	"fmt"
	"testing"
	"time"

	"emporium/internal/policy"
)

// V1.23.0 — US-0001 (Chicago) must be anchored to the exact founding day so
// shop_id 1 is Chicago-on-1986-08-06 at EVERY tier and seed, matching the
// dbo.business_milestones "founding" row and COMPANY_HISTORY.md. Pre-V1.23 the
// founding day was the min of tier-dependent us_first samples — it matched the
// hard-coded milestone only at the 30-sample tiers and drifted at 3g.
func TestFoundingDayAnchoredAcrossTiers(t *testing.T) {
	asOf := time.Date(2016, time.September, 30, 0, 0, 0, 0, time.UTC)
	for tier, total := range policy.ShopCountByTier {
		for _, seed := range []uint64{1, 42, 12345} {
			alloc := allocatePerCountry(total)
			pools := buildDatePools(seed, total, asOf, alloc)
			us := pools["US"]
			if len(us) == 0 {
				t.Fatalf("tier %s seed %d: no US dates generated", tier, seed)
			}
			// Sorted ascending, so us[0] is the earliest US shop → shop_id 1.
			if !us[0].Equal(policy.FoundingDate) {
				t.Errorf("tier %s seed %d: earliest US shop opened %s, want founding day %s",
					tier, seed, us[0].Format("2006-01-02"), policy.FoundingDate.Format("2006-01-02"))
			}
			// V1.24.1 — the flagship (US-0002, New York) is equally canon:
			// second-earliest must be exactly 1986-09-28, and nothing else
			// may open between the two anchors.
			if len(us) >= 2 && !us[1].Equal(policy.FlagshipDate) {
				t.Errorf("tier %s seed %d: second US shop opened %s, want flagship day %s",
					tier, seed, us[1].Format("2006-01-02"), policy.FlagshipDate.Format("2006-01-02"))
			}
			// Nothing in the US pool may predate the founding.
			for _, dte := range us {
				if dte.Before(policy.FoundingDate) {
					t.Errorf("tier %s seed %d: a US shop opened %s, before the founding day",
						tier, seed, dte.Format("2006-01-02"))
				}
			}
		}
	}
}

// V1.19 — the S-curve opening sampler must produce a smooth ramp, not
// the old "1999 cliff" (a 4.4× single-year jump). Sample the 1996-2010
// expansion window and assert the core years rise/fall gently and the
// distribution peaks mid-window (a bell, not uniform, not a wall).
func TestSampleDatesSCurveNoCliff(t *testing.T) {
	start := time.Date(1996, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2010, time.December, 31, 0, 0, 0, 0, time.UTC)
	dates := sampleDatesSCurve(42, "test/scurve", start, end, 6000)
	if len(dates) != 6000 {
		t.Fatalf("got %d dates, want 6000", len(dates))
	}

	byYear := map[int]int{}
	for _, d := range dates {
		byYear[d.Year()]++
	}

	// Core years should ramp gently — no adjacent-year ratio above 2.5
	// in either direction (the cliff was ~4.4×).
	for y := 2000; y <= 2008; y++ {
		a, b := byYear[y-1], byYear[y]
		if a == 0 || b == 0 {
			t.Errorf("core year %d or %d unexpectedly empty (%d, %d)", y-1, y, a, b)
			continue
		}
		hi, lo := a, b
		if b > a {
			hi, lo = b, a
		}
		if float64(hi)/float64(lo) > 2.5 {
			t.Errorf("cliff between %d (%d) and %d (%d): ratio %.2f > 2.5",
				y-1, a, y, b, float64(hi)/float64(lo))
		}
	}

	// Peak must sit mid-window (~2003 for a 1996-2010 bell), not at an edge.
	peakYear, peakCount := 0, -1
	for y, c := range byYear {
		if c > peakCount {
			peakYear, peakCount = y, c
		}
	}
	if peakYear < 2001 || peakYear > 2006 {
		t.Errorf("S-curve peak year %d outside expected mid-window 2001-2006", peakYear)
	}
}

// V1.19 — the early-churn cohort is spread across countries by ShopShares
// and must sum exactly to the requested count, US-heavy.
func TestAllocateChurnSumsToTotal(t *testing.T) {
	const total = 1260 // ~3T churn count
	alloc := allocateChurnPerCountry(total)
	sum := 0
	for _, n := range alloc {
		sum += n
	}
	if sum != total {
		t.Errorf("allocateChurnPerCountry(%d) sums to %d", total, sum)
	}
	if frac := float64(alloc["US"]) / float64(total); frac < 0.50 || frac > 0.60 {
		t.Errorf("US churn share %.3f outside ~0.55 band", frac)
	}
}

// V1.19 — assignClosureSchedule must apply the 2011-2016 wind-down ONLY
// to survivors (shops with no ClosedDate), leaving the pre-closed
// transient cohort untouched, so the 2010-12-31 peak count equals the
// survivor count exactly.
func TestAssignClosureScheduleSkipsTransients(t *testing.T) {
	const nSurvivors, nTransients = 30, 5
	var shopList []Shop
	for i := 0; i < nSurvivors; i++ {
		shopList = append(shopList, Shop{
			ShopCode:   fmt.Sprintf("US-%04d", i+1),
			OpenedDate: fmt.Sprintf("%04d-06-01", 2000+i%9),
		})
	}
	closed := "2008-06-01"
	for i := 0; i < nTransients; i++ {
		shopList = append(shopList, Shop{
			ShopCode:      fmt.Sprintf("US-T%04d", i+1),
			OpenedDate:    "2001-03-01",
			ClosedDate:    &closed,
			ClosureReason: "lease_expiry",
		})
	}

	assignClosureSchedule(42, shopList)

	openOn := func(s Shop, day string) bool {
		if s.OpenedDate > day {
			return false
		}
		return s.ClosedDate == nil || *s.ClosedDate >= day
	}

	survivorsClosed, transientsIntact, openAtPeak := 0, 0, 0
	for _, s := range shopList {
		isTransient := len(s.ShopCode) >= 4 && s.ShopCode[3] == 'T'
		if isTransient {
			if s.ClosedDate != nil && *s.ClosedDate == "2008-06-01" && s.ClosureReason == "lease_expiry" {
				transientsIntact++
			}
		} else {
			if s.ClosedDate == nil {
				t.Errorf("survivor %s was not assigned a closure date", s.ShopCode)
				continue
			}
			yr := (*s.ClosedDate)[:4]
			if yr < "2011" || yr > "2016" {
				t.Errorf("survivor %s closed in %s, expected 2011-2016", s.ShopCode, *s.ClosedDate)
			}
			survivorsClosed++
		}
		if openOn(s, "2010-12-31") {
			openAtPeak++
		}
	}

	if survivorsClosed != nSurvivors {
		t.Errorf("expected %d survivors closed, got %d", nSurvivors, survivorsClosed)
	}
	if transientsIntact != nTransients {
		t.Errorf("expected %d transients untouched, got %d", nTransients, transientsIntact)
	}
	if openAtPeak != nSurvivors {
		t.Errorf("shops open on 2010-12-31 = %d, want survivor count %d", openAtPeak, nSurvivors)
	}
}
