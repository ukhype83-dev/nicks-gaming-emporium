package hr

import (
	"strconv"
	"testing"

	"emporium/internal/policy"
)

// V1.19 — the headline believability fix: the founder/CEO must out-earn
// every shop manager in EVERY year (the pre-V1.19 data inverted this —
// a 1986 CEO earned less than a store manager because the corporate
// maturity damping dragged the founders down to the floor).
func TestCEOAlwaysOutEarnsStoreManager(t *testing.T) {
	ceo := policy.RoleByID(19) // Founder & CEO, G10, corporate
	sm := policy.RoleByID(3)   // Store Manager, G5, retail
	sa := policy.RoleByID(1)   // Sales Associate, G1, retail
	for y := 1986; y <= 2016; y++ {
		ceoWage := annualWageBaseline(ceo, "US", y, true)
		smWage := annualWageBaseline(sm, "US", y, false)
		saWage := annualWageBaseline(sa, "US", y, false)
		if !(ceoWage > smWage) {
			t.Errorf("year %d: CEO %.0f must exceed Store Manager %.0f", y, ceoWage, smWage)
		}
		if !(smWage > saWage) {
			t.Errorf("year %d: Store Manager %.0f must exceed Sales Associate %.0f", y, smWage, saWage)
		}
	}
}

// V1.19 — shop-floor roles share pay grades with corporate roles (a
// Store Manager and a Finance Manager are both G5) but must be paid the
// real, far lower, retail rate. Verify the retail discount at 2025
// (where inflation/FX/maturity are all 1.0).
func TestRetailVsCorporateGradeSplit(t *testing.T) {
	sm := policy.RoleByID(3) // Store Manager, G5, retail
	fm := policy.RoleByID(5) // Finance Manager, G5, corporate
	if sm.DefaultGradeID != fm.DefaultGradeID {
		t.Fatalf("precondition: expected Store Manager and Finance Manager to share a grade, got %d vs %d",
			sm.DefaultGradeID, fm.DefaultGradeID)
	}
	smWage := annualWageBaseline(sm, "US", 2025, false)
	fmWage := annualWageBaseline(fm, "US", 2025, true)
	if !(smWage < fmWage) {
		t.Errorf("Store Manager %.0f should be well below Finance Manager %.0f despite same grade", smWage, fmWage)
	}
	// Store Manager should land near the real ~$55k, Finance Manager ~$130k.
	if smWage < 45_000 || smWage > 65_000 {
		t.Errorf("Store Manager 2025 wage %.0f outside realistic ~$55k band", smWage)
	}
	if fmWage < 115_000 || fmWage > 145_000 {
		t.Errorf("Finance Manager 2025 wage %.0f outside realistic ~$130k band", fmWage)
	}
}

// V1.19 — the annual bonus is grade-scaled and profit-gated: present in
// profitable years (1996-2010 full, 2011-2012 reduced), absent once the
// company is losing money (2013-2016), and never for entry-level (G1)
// hourly staff.
func TestBonusProfitGating(t *testing.T) {
	mgr := Person{
		PersonID:           1,
		CountryOfResidence: "US",
		Spell: EmploymentSpell{
			SpellID:    1,
			RoleID:     10, // Engineering Manager, G7 (bonus 0.30), corporate
			HomeShopID: nil,
			StartedAt:  "2000-06-01",
		},
	}
	var hid int64
	recs := generateCompensation(mgr, 42, &hid)

	bonusYears := map[int]int{} // year -> count of bonus rows
	for _, r := range recs {
		if r.ChangeReason != "bonus" {
			continue
		}
		// single-day record
		if r.EffectiveTo == nil || *r.EffectiveTo != r.EffectiveFrom {
			t.Errorf("bonus row not single-day: from=%s to=%v", r.EffectiveFrom, r.EffectiveTo)
		}
		yr, err := strconv.Atoi(r.EffectiveFrom[:4])
		if err != nil {
			t.Fatalf("bad effective_from %q: %v", r.EffectiveFrom, err)
		}
		bonusYears[yr]++
	}

	mustHave := []int{2005, 2010, 2011, 2012}
	for _, y := range mustHave {
		if bonusYears[y] == 0 {
			t.Errorf("expected a bonus row in profitable year %d, found none", y)
		}
	}
	mustNotHave := []int{2000, 2013, 2014, 2015, 2016}
	for _, y := range mustNotHave {
		if bonusYears[y] != 0 {
			t.Errorf("expected NO bonus row in year %d, found %d", y, bonusYears[y])
		}
	}

	// Entry-level retail associate (G1) never gets a bonus.
	assoc := Person{
		PersonID:           2,
		CountryOfResidence: "US",
		Spell: EmploymentSpell{
			SpellID:    2,
			RoleID:     1, // Sales Associate, G1 (bonus 0.0), retail
			HomeShopID: ptrInt64(123),
			StartedAt:  "2003-01-01",
		},
	}
	var hid2 int64
	for _, r := range generateCompensation(assoc, 42, &hid2) {
		if r.ChangeReason == "bonus" {
			t.Errorf("G1 associate should never receive a bonus, found one dated %s", r.EffectiveFrom)
		}
	}
}

// V1.19 — the bonus must NOT compound into base pay: the recurring
// (hire/cola/tenure) wage progression must be identical whether or not
// bonus rows are interleaved. Verify the base records form a clean
// COLA progression unaffected by the bonus lumps.
func TestBonusNonCompounding(t *testing.T) {
	// End the spell before 2014 so no retention_2014 (+20%) or wind-down
	// uplift sits between COLA steps — leaving a clean COLA-only base
	// progression to verify the interleaved bonus lumps don't compound.
	end := "2010-12-31"
	mgr := Person{
		PersonID:           3,
		CountryOfResidence: "US",
		Spell: EmploymentSpell{
			SpellID:    3,
			RoleID:     10, // Engineering Manager, G7, corporate
			HomeShopID: nil,
			StartedAt:  "2001-01-01",
			EndedAt:    &end,
		},
	}
	var hid int64
	recs := generateCompensation(mgr, 42, &hid)

	sawBonus := false
	var prev float64
	first := true
	for _, r := range recs {
		if r.ChangeReason == "bonus" {
			sawBonus = true
			continue
		}
		if r.ChangeReason != "cola" && r.ChangeReason != "tenure_step" {
			continue
		}
		if !first {
			ratio := r.AnnualWage / prev
			// Each step is base × (1 + COLA + tenure). For a 2001 hire
			// (post-2000) tenure premium is 0, so the step is +COLA only.
			want := 1.0 + policy.WageCOLAPerYear
			if ratio < want-0.005 || ratio > want+0.005 {
				t.Errorf("base COLA step ratio %.4f != %.4f — bonus appears to have compounded into base pay", ratio, want)
			}
		}
		prev = r.AnnualWage
		first = false
	}
	if !sawBonus {
		t.Error("expected interleaved bonus rows for this profitable-era manager, found none")
	}
}

func ptrInt64(v int64) *int64 { return &v }
