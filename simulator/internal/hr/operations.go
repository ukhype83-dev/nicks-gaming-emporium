// HR operations records emitted alongside each employment spell:
//
//   - hr.contracts       — one per spell (occasionally amendments)
//   - hr.staff_shifts    — weekly schedule for retail spells; sparse
//     sample to keep volume realistic without 35-year exhaustion
//   - hr.payroll_runs    — generated independently per (country, period)
//     by GeneratePayrollRuns; runs FK to retail.countries already loaded
//   - hr.payroll_lines   — one per active spell per run
//
// payroll_runs is the only HR table not derivable from a single Person —
// it's a country/calendar grid. So GeneratePayrollRuns is a separate
// entry point invoked once per Generate cycle, before payroll_lines
// can be emitted (lines FK to runs).
package hr

import (
	"fmt"
	"math/rand/v2"
	"time"

	"emporium/internal/policy"
	"emporium/internal/rng"
	"emporium/internal/shops"
)

// --------------------------------------------------------------------
// Type definitions
// --------------------------------------------------------------------

// Contract maps to hr.contracts. One per spell typically.
type Contract struct {
	ContractID    int64    `json:"contract_id"`
	ContractType  string   `json:"contract_type"`
	WeeklyHours   *float64 `json:"weekly_hours"`
	SignedAt      string   `json:"signed_at"`
	SourceSystem  string   `json:"source_system"`
}

// StaffShift maps to hr.staff_shifts.
type StaffShift struct {
	ShiftID       int64  `json:"shift_id"`
	ShopID        int64  `json:"shop_id"`
	ShiftStart    string `json:"shift_start"`
	ShiftEnd      string `json:"shift_end"`
	BreakMinutes  int    `json:"break_minutes"`
	SourceSystem  string `json:"source_system"`
}

// PayrollRun maps to hr.payroll_runs. Generated once per Generate
// cycle by GeneratePayrollRuns, not per-Person.
type PayrollRun struct {
	PayrollRunID  int64  `json:"payroll_run_id"`
	CountryCode   string `json:"country_code"`
	PeriodStart   string `json:"period_start"`
	PeriodEnd     string `json:"period_end"`
	PaidAt        string `json:"paid_at"`
	CurrencyCode  string `json:"currency_code"`
	Status        string `json:"status"`
	SourceSystem  string `json:"source_system"`
}

// PayrollLine maps to hr.payroll_lines.
type PayrollLine struct {
	PayrollLineID    int64   `json:"payroll_line_id"`
	PayrollRunID     int64   `json:"payroll_run_id"`
	Gross            float64 `json:"gross"`
	Tax              float64 `json:"tax"`
	EmployeeContribs float64 `json:"employee_contribs"`
	EmployerContribs float64 `json:"employer_contribs"`
	Net              float64 `json:"net"`
}

// Shift-history retention. Real HR systems retain ~5-7 years of shift
// records; older shifts are archived/aggregated. Keeps simulator
// volume realistic without losing the join-graph behaviour.
const shiftHistoryYears = 5

// --------------------------------------------------------------------
// Per-Person generation (called from emitRetailStaff / emitHQStaff)
// --------------------------------------------------------------------

// hrCounters tracks the next ID per HR-ops table. Passed by pointer
// across all spells in a Generate run.
type hrCounters struct {
	contract     int64
	shift        int64
	payrollLine  int64
}

// generateContracts emits one contract per spell, signed within the
// week of the spell's start.
func generateContracts(spell *EmploymentSpell, ctr *hrCounters, r *rand.Rand) []Contract {
	contractType, hours := pickContractType(spell.HomeShopID != nil, r)
	signedAt := spell.StartedAt
	// Tiny chance of being signed a few days before/after start.
	if t, err := time.Parse("2006-01-02", spell.StartedAt); err == nil {
		offset := r.IntN(7) - 3
		signedAt = t.AddDate(0, 0, offset).Format("2006-01-02")
	}
	ctr.contract++
	return []Contract{{
		ContractID:   ctr.contract,
		ContractType: contractType,
		WeeklyHours:  hours,
		SignedAt:     signedAt,
		SourceSystem: spell.SourceSystem,
	}}
}

// pickContractType samples contract type and matching weekly hours.
// Retail staff skew toward part-time; HQ heavy on permanent_full.
func pickContractType(isRetail bool, r *rand.Rand) (string, *float64) {
	u := r.Float64()
	var ct string
	if isRetail {
		switch {
		case u < 0.45:
			ct = "permanent_full"
		case u < 0.80:
			ct = "permanent_part"
		case u < 0.92:
			ct = "fixed_term"
		case u < 0.99:
			ct = "temp"
		default:
			ct = "intern"
		}
	} else {
		switch {
		case u < 0.85:
			ct = "permanent_full"
		case u < 0.92:
			ct = "permanent_part"
		case u < 0.98:
			ct = "fixed_term"
		default:
			ct = "intern"
		}
	}
	var hours float64
	switch ct {
	case "permanent_full":
		hours = 35 + r.Float64()*5 // 35-40
	case "permanent_part":
		hours = 16 + r.Float64()*9 // 16-25
	case "fixed_term":
		hours = 30 + r.Float64()*10 // 30-40
	case "temp":
		hours = 24 + r.Float64()*16 // 24-40
	case "intern":
		hours = 30 + r.Float64()*10 // 30-40
	}
	rounded := float64(int(hours*100)) / 100
	return ct, &rounded
}

// generateShifts emits shift rows for retail spells over the last
// `shiftHistoryYears` years. HQ spells (no HomeShopID) get nothing —
// HQ employees don't punch shifts at a shop.
//
// Per week: 4-5 shifts for full-time spells, 2-3 for part-time. Each
// shift is 6-9 hours starting between 08:00 and 14:00.
func generateShifts(spell *EmploymentSpell, hours float64, asOf time.Time, ctr *hrCounters, r *rand.Rand) []StaffShift {
	if spell.HomeShopID == nil {
		return nil
	}
	started, err := time.Parse("2006-01-02", spell.StartedAt)
	if err != nil {
		return nil
	}
	end := asOf
	if spell.EndedAt != nil {
		if e, err := time.Parse("2006-01-02", *spell.EndedAt); err == nil {
			end = e
		}
	}
	if !end.After(started) {
		return nil
	}
	// Cap to last shiftHistoryYears.
	earliest := asOf.AddDate(-shiftHistoryYears, 0, 0)
	if started.Before(earliest) {
		started = earliest
	}
	if !end.After(started) {
		return nil
	}
	// Walk weeks. Number of shifts depends on contracted hours.
	shiftsPerWeek := 4
	if hours < 26 {
		shiftsPerWeek = 3
	}
	out := make([]StaffShift, 0, int(end.Sub(started).Hours()/(7*24))*shiftsPerWeek)
	week := started
	for week.Before(end) {
		// Roll actual count for this week (cover absences/holidays).
		count := shiftsPerWeek
		if r.Float64() < 0.10 {
			count -= 1 // sick day / holiday
		}
		if count <= 0 {
			week = week.AddDate(0, 0, 7)
			continue
		}
		// Pick `count` distinct days of the week (Mon-Sun = 0-6).
		days := pickDistinct(7, count, r)
		for _, d := range days {
			day := week.AddDate(0, 0, d)
			if day.After(end) {
				continue
			}
			startHour := 8 + r.IntN(7) // 08:00-14:00
			startMin := 0
			if r.Float64() < 0.5 {
				startMin = 30
			}
			shiftStart := time.Date(day.Year(), day.Month(), day.Day(),
				startHour, startMin, 0, 0, time.UTC)
			length := 6 + r.IntN(4) // 6-9 hours
			shiftEnd := shiftStart.Add(time.Duration(length) * time.Hour)
			breakMin := 15
			if length >= 7 {
				breakMin = 30
			}
			ctr.shift++
			out = append(out, StaffShift{
				ShiftID:      ctr.shift,
				ShopID:       *spell.HomeShopID,
				ShiftStart:   shiftStart.Format("2006-01-02T15:04:05.000Z"),
				ShiftEnd:     shiftEnd.Format("2006-01-02T15:04:05.000Z"),
				BreakMinutes: breakMin,
				SourceSystem: spell.SourceSystem,
			})
		}
		week = week.AddDate(0, 0, 7)
	}
	return out
}

// pickDistinct returns `count` distinct ints in [0, n) without replacement.
// Order is unspecified but deterministic given the RNG.
func pickDistinct(n, count int, r *rand.Rand) []int {
	if count >= n {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	}
	pool := make([]int, n)
	for i := range pool {
		pool[i] = i
	}
	for i := 0; i < count; i++ {
		j := i + r.IntN(n-i)
		pool[i], pool[j] = pool[j], pool[i]
	}
	return pool[:count]
}

// generatePayrollLines emits one PayrollLine per active payroll run
// in the spell's country, scoped to the spell's date span.
func generatePayrollLines(
	spell *EmploymentSpell,
	weeklyHours float64,
	country string,
	runs []PayrollRun,
	ctr *hrCounters,
	r *rand.Rand,
) []PayrollLine {
	started, err := time.Parse("2006-01-02", spell.StartedAt)
	if err != nil {
		return nil
	}
	var ended time.Time
	if spell.EndedAt != nil {
		if e, err := time.Parse("2006-01-02", *spell.EndedAt); err == nil {
			ended = e
		}
	}
	// V1.9.0: hourly rate is now computed per period (not once per
	// person) so that wage inflation can be applied — a 1986 SA gets
	// 1986-USD-equivalent pay, not 2025 pay. Earlier versions held
	// hourly rate constant across a person's entire career, which made
	// long-tenure employees underpaid relative to design intent.
	taxRate := taxRateFor(country)
	out := make([]PayrollLine, 0, len(runs))
	for _, run := range runs {
		if run.CountryCode != country {
			continue
		}
		ps, _ := time.Parse("2006-01-02", run.PeriodStart)
		pe, _ := time.Parse("2006-01-02", run.PeriodEnd)
		// Spell must be active for at least part of the period.
		if started.After(pe) {
			continue
		}
		if !ended.IsZero() && ended.Before(ps) {
			continue
		}
		periodDays := pe.Sub(ps).Hours() / 24
		if periodDays < 1 {
			periodDays = 1
		}
		periodYear := ps.Year()
		hourlyRate := payRateFor(spell.PayGradeID, country, periodYear, r)
		gross := weeklyHours * (periodDays / 7) * hourlyRate
		// Slight randomness — overtime, deductions, sick days.
		gross *= 0.92 + r.Float64()*0.16
		gross = roundCurrency(gross)
		tax := roundCurrency(gross * taxRate)
		empContribs := roundCurrency(gross * 0.05)
		erContribs := roundCurrency(gross * 0.08)
		net := roundCurrency(gross - tax - empContribs)
		ctr.payrollLine++
		out = append(out, PayrollLine{
			PayrollLineID:    ctr.payrollLine,
			PayrollRunID:     run.PayrollRunID,
			Gross:            gross,
			Tax:              tax,
			EmployeeContribs: empContribs,
			EmployerContribs: erContribs,
			Net:              net,
		})
	}
	return out
}

func roundCurrency(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// payRateFor returns an hourly rate by pay grade for a given year and
// country, scaled to local currency rough-equivalent of USD baseline.
//
// V1.9.0 changes:
//   - Pay-grade scale compressed: was [15, 20, 28, 38, 55, 75, 100,
//     140, 200, 350] giving G5/G1 = 3.67× (which compounded with PT
//     contracts to a 7.4× observed gap). Now [15, 17, 20, 24, 32, 50,
//     75, 110, 160, 250] giving G5/G1 = 2.13× — matches real retail.
//   - Year-aware inflation: rate is scaled by
//     policy.WageInflationFactor(country, year). A 1990 US SA now
//     receives ~$6.40/hr (≈ $15 × 0.42), matching real 1990 retail.
//   - HQ leadership (G6-G10) still pulls big numbers but compressed
//     relative to V1.7/V1.8 — a Senior Director at $110/hr × 40 hrs
//     × 52 ≈ $228K/yr, a VP at $160/hr ≈ $333K/yr.
func payRateFor(gradeID int, country string, year int, r *rand.Rand) float64 {
	// USD baseline rates per grade (per hour, 2025 levels).
	base := []float64{15, 17, 20, 24, 32, 50, 75, 110, 160, 250}
	if gradeID < 1 || gradeID > len(base) {
		gradeID = 1
	}
	usdRate := base[gradeID-1]
	usdRate *= 0.9 + r.Float64()*0.2 // ±10% variance
	// V1.9.0: apply year-based inflation curve.
	usdRate *= policy.WageInflationFactor(country, year)
	// Currency conversion proxy — match policy.BasePriceByCurrency
	// roughly. Local currency / 50 USD = local-rate-multiplier.
	bp := policy.BasePriceByCurrency[currencyForCountry(country)]
	if bp == 0 {
		bp = 50
	}
	return usdRate * (bp / 50.0)
}

func taxRateFor(country string) float64 {
	// Rough effective income+contributions rate by country.
	switch country {
	case "US", "CA":
		return 0.22
	case "GB":
		return 0.25
	case "DE", "FR", "IT", "ES", "NL", "BE":
		return 0.32
	case "JP", "KR":
		return 0.20
	case "SE", "NO", "DK":
		return 0.35
	case "BR":
		return 0.15
	default:
		return 0.25
	}
}

func currencyForCountry(country string) string {
	for _, s := range policy.ShopShares {
		if s.Country == country {
			return s.CurrencyCode
		}
	}
	return "USD"
}

// --------------------------------------------------------------------
// Independent generation: payroll_runs
// --------------------------------------------------------------------

// PayrollSchedule defines how often a country runs payroll.
type payrollSchedule struct {
	periodsPerYear int
	currency       string
}

// payrollSchedules describes per-country pay frequency. Bi-weekly for
// US/CA/UK/AU; monthly for most of EU/JP/etc; Italy gets 14 (13th and
// 14th month bonuses).
var payrollSchedules = map[string]payrollSchedule{
	"US": {26, "USD"},
	"CA": {26, "CAD"},
	"GB": {26, "GBP"},
	"AU": {26, "AUD"},
	"DE": {12, "EUR"},
	"FR": {12, "EUR"},
	"ES": {12, "EUR"},
	"NL": {12, "EUR"},
	"CH": {12, "CHF"},
	"IT": {14, "EUR"},
	"JP": {12, "JPY"},
	"KR": {12, "KRW"},
	"BR": {12, "BRL"},
	"PL": {12, "PLN"},
	"CZ": {12, "CZK"},
	"SE": {12, "SEK"},
	"NO": {12, "NOK"},
	"DK": {12, "DKK"},
}

// GeneratePayrollRuns emits payroll runs for every (country with a
// shop, calendar period) pair from the earliest shop opening through
// asOf. Each run is one row; runs are scheduled per country pay
// frequency.
//
// Independent of person/spell generation — call once per Generate
// cycle. Returns the slice of runs so payroll_lines generation can
// FK into it.
func GeneratePayrollRuns(seed uint64, asOf time.Time, shopList []shops.Shop) []PayrollRun {
	earliestByCountry := earliestShopByCountry(shopList)
	out := make([]PayrollRun, 0, len(earliestByCountry)*40*26)
	var runID int64
	for country, earliest := range earliestByCountry {
		sched, ok := payrollSchedules[country]
		if !ok {
			continue
		}
		_ = rng.Derive(seed, "hr/payroll/"+country) // reserved for future variance
		runs := buildRunsForCountry(country, earliest, asOf, sched, &runID)
		out = append(out, runs...)
	}
	return out
}

func earliestShopByCountry(shopList []shops.Shop) map[string]time.Time {
	out := make(map[string]time.Time)
	for _, s := range shopList {
		opened, err := time.Parse("2006-01-02", s.OpenedDate)
		if err != nil {
			continue
		}
		if existing, ok := out[s.CountryCode]; !ok || opened.Before(existing) {
			out[s.CountryCode] = opened
		}
	}
	return out
}

func buildRunsForCountry(country string, earliest, asOf time.Time, sched payrollSchedule, runID *int64) []PayrollRun {
	out := make([]PayrollRun, 0, 40*sched.periodsPerYear)
	periodLen := 365 / sched.periodsPerYear
	current := earliest
	source := "hr_paper_1986_1998"
	for !current.After(asOf) {
		periodEnd := current.AddDate(0, 0, periodLen-1)
		if periodEnd.After(asOf) {
			periodEnd = asOf
		}
		switch {
		case current.Year() <= 1998:
			source = "hr_paper_1986_1998"
		case current.Year() <= 2013:
			source = "hr_legacy_hris_1999_2013"
		default:
			source = "hr_unified_2014_plus"
		}
		paidAt := periodEnd.AddDate(0, 0, 5) // 5 working days after period end
		if paidAt.After(asOf) {
			paidAt = asOf
		}
		*runID++
		out = append(out, PayrollRun{
			PayrollRunID:  *runID,
			CountryCode:   country,
			PeriodStart:   current.Format("2006-01-02"),
			PeriodEnd:     periodEnd.Format("2006-01-02"),
			PaidAt:        paidAt.Format("2006-01-02"),
			CurrencyCode:  sched.currency,
			Status:        "posted",
			SourceSystem:  source,
		})
		current = current.AddDate(0, 0, periodLen)
	}
	return out
}

// PayrollRunsByCountry returns a map indexing the pre-generated run
// slice by country for fast lookup at line-generation time.
func PayrollRunsByCountry(runs []PayrollRun) map[string][]PayrollRun {
	out := make(map[string][]PayrollRun, 20)
	for _, r := range runs {
		out[r.CountryCode] = append(out[r.CountryCode], r)
	}
	return out
}

// Wrapper for fmt to satisfy goimports if no other use of fmt — silenced.
var _ = fmt.Sprintf
