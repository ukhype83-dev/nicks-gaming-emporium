// V1.15.0 — compensation history.
//
// Produces a per-person wage history with annual COLA, tenure
// premiums for pre-2000 hires, retention bonuses 2014-2016 for ~15%
// of staff, severance lumps at termination cohorts, and wind-down
// crew premiums.
//
// V1.19 — believability recalibration: per-person pay is now realistic
// (see annualWageBaseline — real era-correct levels, executives above
// managers), and every staffer above entry level earns a grade-scaled
// ANNUAL BONUS in profitable years (cut to zero 2013-2016). The
// death-spiral is unchanged in shape: wage cost compounds (~5%/yr after
// COLA + tenure) even as revenue declines, pushing wages-as-%-of-revenue
// from a healthy band (~11-15%) to over 100% by 2016 — now held there by
// realistic HEADCOUNT (policy.HeadcountForShop) rather than the old
// ×1.55 per-person fudge.

package hr

import (
	"fmt"
	"math"
	"sort"
	"time"

	"emporium/internal/policy"
	"emporium/internal/rng"
)

// CompensationRecord mirrors hr.compensation_history.
type CompensationRecord struct {
	HistoryID     int64   `json:"history_id"`
	PersonID      int64   `json:"person_id"`
	EffectiveFrom string  `json:"effective_from"`
	EffectiveTo   *string `json:"effective_to"`
	AnnualWage    float64 `json:"annual_wage"`
	CurrencyCode  string  `json:"currency_code"`
	ChangeReason  string  `json:"change_reason"`
}

// generateCompensation builds the full wage history for a person.
// Deterministic per personID. Records are returned in chronological
// order with consistent effective_from / effective_to bounds.
//
// Strategy: collect all comp-change events first (hire, COLA per
// year, retention bonus, wind-down premium), sort by effective date,
// then walk in order applying multiplicative wage adjustments. This
// guarantees out-of-order events (e.g. retention bonus dated 2014-09
// inserted after the function already walked 2014-2016 COLA records)
// can't violate the schema's effective_to >= effective_from check.
func generateCompensation(p Person, seed uint64, historyID *int64) []CompensationRecord {
	started, err := time.Parse("2006-01-02", p.Spell.StartedAt)
	if err != nil {
		return nil
	}
	var ended *time.Time
	if p.Spell.EndedAt != nil {
		if t, err := time.Parse("2006-01-02", *p.Spell.EndedAt); err == nil {
			ended = &t
		}
	}

	r := rng.Derive(seed, fmt.Sprintf("hr/comp/%d", p.PersonID))

	currency := currencyForCountry(p.CountryOfResidence)
	role := policy.RoleByID(p.Spell.RoleID)
	// V1.16.5: corporate (HQ / senior / founder) staff are wage-damped
	// in the early years — a bootstrapped retailer pays owner-operator /
	// equity-culture rates until it reaches scale. Retail shop staff
	// (HomeShopID set) always earned market hourly pay, so they're
	// exempt. See policy.CorporateMaturityFactor.
	isCorporate := p.Spell.HomeShopID == nil
	baseAnnual := annualWageBaseline(role, p.CountryOfResidence, started.Year(), isCorporate)

	isRetentionEligible := r.Float64() < policy.RetentionBonusEligibleFraction
	isWindDown := p.Spell.TerminationReason != nil && *p.Spell.TerminationReason == "liquidation_2016"

	tenureRatePerYear := 0.0
	if started.Year() < policy.WageTenurePremiumCutoffYear {
		tenureRatePerYear = policy.WageTenurePremiumPerYear
	}

	endWalk := policy.BusinessClosureDate
	if ended != nil && ended.Before(endWalk) {
		endWalk = *ended
	}

	type compEvent struct {
		date time.Time
		kind string // hire | cola | retention | winddown
	}
	events := []compEvent{{started, "hire"}}

	// Annual COLA / tenure step at Jan 1 of each year after the hire year.
	for y := started.Year() + 1; y <= endWalk.Year(); y++ {
		d := time.Date(y, time.January, 1, 0, 0, 0, 0, time.UTC)
		if !d.After(endWalk) {
			events = append(events, compEvent{d, "cola"})
		}
	}
	if isRetentionEligible {
		d := policy.RetentionBonusEffectiveFrom
		if !d.Before(started) && !d.After(endWalk) {
			events = append(events, compEvent{d, "retention"})
		}
	}
	if isWindDown {
		d := time.Date(2016, time.April, 1, 0, 0, 0, 0, time.UTC)
		if !d.Before(started) && !d.After(endWalk) {
			events = append(events, compEvent{d, "winddown"})
		}
	}

	sort.SliceStable(events, func(i, j int) bool {
		return events[i].date.Before(events[j].date)
	})

	out := make([]CompensationRecord, 0, len(events)+1)
	currentWage := baseAnnual
	for i, e := range events {
		var reason string
		switch e.kind {
		case "hire":
			reason = "hire"
		case "cola":
			currentWage *= (1.0 + policy.WageCOLAPerYear + tenureRatePerYear)
			reason = "cola"
			if tenureRatePerYear > 0 {
				reason = "tenure_step"
			}
		case "retention":
			currentWage *= (1.0 + policy.RetentionBonusUplift)
			reason = "retention_2014"
		case "winddown":
			currentWage *= (1.0 + policy.WindDownCrewPremium)
			reason = "winddown_premium"
		}
		*historyID++
		rec := CompensationRecord{
			HistoryID:     *historyID,
			PersonID:      p.PersonID,
			EffectiveFrom: e.date.Format("2006-01-02"),
			AnnualWage:    math.Round(currentWage*100) / 100,
			CurrencyCode:  currency,
			ChangeReason:  reason,
		}
		// effective_to = (next event date - 1), clamped to same-day to
		// satisfy the check constraint when two events fall on the same
		// calendar date.
		if i+1 < len(events) {
			next := events[i+1].date
			prevDay := next.AddDate(0, 0, -1)
			if prevDay.Before(e.date) {
				prevDay = e.date
			}
			s := prevDay.Format("2006-01-02")
			rec.EffectiveTo = &s
		}
		out = append(out, rec)
	}

	// Close the trailing record at ended_at (if person left).
	if ended != nil && len(out) > 0 && out[len(out)-1].EffectiveTo == nil {
		s := ended.Format("2006-01-02")
		out[len(out)-1].EffectiveTo = &s
	}

	// V1.19 — annual performance bonus. Grade-scaled, profit-gated, paid
	// each March for the prior fiscal year. Emitted as standalone
	// single-day records that do NOT compound into base pay (like the
	// severance lump below). The bonus is sized off the base wage in
	// effect on the bonus date, looked up from the base records (out)
	// built above. No RNG draws — pure function of grade/year/wage.
	if bf := policy.BonusFractionForGrade(role.DefaultGradeID); bf > 0 {
		numBase := len(out)
		for y := started.Year(); y <= endWalk.Year(); y++ {
			payout := policy.BonusPayoutFactorForYear(y)
			if payout <= 0 {
				continue
			}
			d := time.Date(y, time.March, 1, 0, 0, 0, 0, time.UTC)
			if d.Before(started) || d.After(endWalk) {
				continue
			}
			wage, ok := wageInEffectAt(out[:numBase], d)
			if !ok {
				continue
			}
			amount := wage * bf * payout
			if amount <= 0 {
				continue
			}
			*historyID++
			s := d.Format("2006-01-02")
			out = append(out, CompensationRecord{
				HistoryID:     *historyID,
				PersonID:      p.PersonID,
				EffectiveFrom: s,
				EffectiveTo:   &s,
				AnnualWage:    math.Round(amount*100) / 100,
				CurrencyCode:  currency,
				ChangeReason:  "bonus",
			})
		}
	}

	// Severance lump on termination — single-day record with lump in
	// annual_wage, change_reason marking it as a one-off.
	if ended != nil && p.Spell.TerminationReason != nil {
		severance := computeSeverance(currentWage, started, *ended, *p.Spell.TerminationReason)
		if severance > 0 {
			*historyID++
			s := ended.Format("2006-01-02")
			out = append(out, CompensationRecord{
				HistoryID:     *historyID,
				PersonID:      p.PersonID,
				EffectiveFrom: s,
				EffectiveTo:   &s,
				AnnualWage:    math.Round(severance*100) / 100,
				CurrencyCode:  currency,
				ChangeReason:  severanceReasonForTermination(*p.Spell.TerminationReason),
			})
		}
	}

	return out
}

// computeSeverance returns the lump-sum severance in local currency
// based on tenure (weeks per year of service, capped) and termination
// reason. Resignations get nothing.
func computeSeverance(annualWage float64, started, ended time.Time, reason string) float64 {
	switch reason {
	case "restructuring_2014",
		"restructuring_2015",
		"chapter11_2016",
		"liquidation_2016":
		// Eligible cohorts.
	default:
		return 0
	}
	years := ended.Sub(started).Hours() / (24 * 365.25)
	if years < 1 {
		return 0
	}
	weeks := years * float64(policy.SeveranceWeeksPerYearOfService)
	if weeks > float64(policy.SeveranceWeeksMax) {
		weeks = float64(policy.SeveranceWeeksMax)
	}
	weeklyWage := annualWage / 52.0
	return weeks * weeklyWage
}

// wageInEffectAt returns the base annual wage in effect on a given date
// by scanning the (chronological) base compensation records. Used to
// size the non-compounding annual bonus off the salary at bonus time.
// Dates are ISO "2006-01-02" strings, so lexical comparison == temporal.
func wageInEffectAt(recs []CompensationRecord, at time.Time) (float64, bool) {
	atStr := at.Format("2006-01-02")
	wage, found := 0.0, false
	for _, r := range recs {
		if r.EffectiveFrom <= atStr && (r.EffectiveTo == nil || *r.EffectiveTo >= atStr) {
			wage, found = r.AnnualWage, true
		}
	}
	return wage, found
}

func severanceReasonForTermination(termReason string) string {
	switch termReason {
	case "restructuring_2014":
		return "severance_2014"
	case "restructuring_2015":
		return "severance_2015"
	case "chapter11_2016", "liquidation_2016":
		return "severance_2016"
	default:
		return "severance"
	}
}

// annualWageBaseline returns a country/role/year-appropriate annual
// wage in local currency. Combines a per-grade USD baseline, a retail
// discount for shop-floor roles, a PPP/wage-level country factor, and
// year-of-hire wage inflation.
//
// V1.19 — full realism recalibration:
//   - The G1-G10 USD anchors are real corporate pay levels (G10 exec
//     $1.05M, up from $775k, so executives out-earn managers).
//   - Pay grades are SHARED retail↔corporate (a Store Manager and a
//     Finance Manager are both G5). retailFactorByGrade discounts the
//     shop-floor roles so a Store Manager lands ~$55k (2025) while a
//     Finance Manager stays ~$130k.
//   - Founders (CEO/Co-Founder) use the gentler FounderDrawFactor
//     instead of CorporateMaturityFactor so they're never inverted
//     below a store manager (the pre-V1.19 bug: 1986 CEO < store mgr).
//
// The backward WageInflationFactor still scales these 2025 anchors to
// era-nominal levels for the hire year. The old V1.15.2 ×1.55 fudge is
// gone — the aggregate wages-of-revenue death-spiral curve is now held
// realistic by the higher headcount (policy.HeadcountForShop) rather
// than by inflated per-person pay.
func annualWageBaseline(role policy.Role, country string, year int, isCorporate bool) float64 {
	// Real 2025-USD anchors by pay grade (corporate-referenced).
	usdByGrade := map[int]float64{
		1:  42000,   // Associate / Warehouse / CSR
		2:  70000,   // Financial Analyst
		3:  82000,   // Team Lead / Recruiter / Mkt Coordinator
		4:  105000,  // Software Engineer / CS Team Lead
		5:  130000,  // Finance/Marketing Manager / Buyer / HR BP
		6:  175000,  // Senior Engineer / Senior Buyer
		7:  240000,  // Director / Engineering Manager
		8:  360000,  // Senior Director
		9:  520000,  // Vice President
		10: 1050000, // Executive / Founder CEO
	}
	usd := usdByGrade[role.DefaultGradeID]
	if usd == 0 {
		usd = 62000
	}

	// Shop-floor roles sit well below the corporate grade anchor they
	// nominally share (a Store Manager is not paid like a Finance
	// Manager). Apply the retail discount.
	if role.IsRetailStaff {
		if f, ok := retailFactorByGrade[role.DefaultGradeID]; ok {
			usd *= f
		}
	}

	// Country wage-level factor (vs US baseline).
	countryFactor := wageLevelFactor(country)

	// Year-of-hire inflation (2025 anchor → era-nominal).
	inflationFactor := policy.WageInflationFactor(country, year)

	// Local-currency conversion: rough constant FX. The simulator's
	// FX tables (retail.fx_rates) exist but using them here would
	// over-complicate the comp generator. Wage figures here are
	// stored in the person's local currency, so apply a coarse FX
	// table to convert USD baseline → local.
	fx := fxToLocal(country)

	base := usd * countryFactor * inflationFactor * fx

	// V1.16.5: damp corporate wages in the early (sub-scale) years.
	// V1.19: founders get a gentler ramp (FounderDrawFactor) so they
	// always out-earn shop managers.
	if isCorporate {
		if policy.IsFounderRole(role.ID) {
			base *= policy.FounderDrawFactor(year)
		} else {
			base *= policy.CorporateMaturityFactor(year)
		}
	}
	return base
}

// retailFactorByGrade discounts shop-floor roles below the corporate
// grade anchor they nominally share, hitting real 2025 retail pay:
// Sales Associate (G1) ~$30k, Assistant Manager (G4) ~$42k, Store
// Manager (G5) ~$55k. Corporate G1/G4/G5 roles (Warehouse, CSR,
// Software Engineer, Finance Manager, ...) are NOT retail and keep the
// full anchor. A single scalar can't hit all three targets, hence the
// per-grade map.
var retailFactorByGrade = map[int]float64{
	1: 0.72, // Sales Associate   → 42000 × 0.72 ≈ 30k
	4: 0.40, // Assistant Manager → 105000 × 0.40 = 42k
	5: 0.42, // Store Manager     → 130000 × 0.42 ≈ 55k
}

func wageLevelFactor(country string) float64 {
	// Rough PPP-adjusted wage-level factor vs US=1.0.
	switch country {
	case "US":
		return 1.00
	case "CH", "NO":
		return 1.10
	case "DK", "SE", "DE", "NL":
		return 0.85
	case "GB", "FR", "IT", "ES", "CA", "AU":
		return 0.80
	case "JP":
		return 0.75
	case "KR":
		return 0.60
	case "PL", "CZ":
		return 0.45
	case "BR":
		return 0.35
	default:
		return 0.70
	}
}

func fxToLocal(country string) float64 {
	// Rough USD→local multipliers (constant; not historically varying).
	switch country {
	case "US", "CA", "AU":
		return 1.0
	case "GB":
		return 0.78
	case "JP":
		return 145.0
	case "KR":
		return 1325.0
	case "BR":
		return 5.1
	case "DE", "FR", "IT", "ES", "NL":
		return 0.92
	case "PL":
		return 4.0
	case "CZ":
		return 23.0
	case "SE", "NO", "DK":
		return 10.5
	case "CH":
		return 0.91
	default:
		return 1.0
	}
}

func ptrString(s string) *string { return &s }
