// Package hr generates HR records deterministically per §9.11.
//
// Current scope (V1 skeleton):
//   - Retail staff — PerShopHeadcount per shop, 1 SM + 1 AM + 8 SA
//   - HQ staff — HQStaffRatio above retail total, distributed across
//     non-retail roles per HQRoleWeights, US-based for V1
//   - One open employment_spell per person (no rehires or terminations
//     yet — V1.1 adds those)
//   - One current staff_address per person (home address)
//   - DOB derived so age-at-start is 18–65, median ~32
//
// Deferred to V1.1:
//   - Past spells (turnover ~40% per year in retail, so historical
//     spells dwarf current ones at large tiers)
//   - Contracts, payroll_runs, payroll_lines, staff_shifts
//   - Performance reviews, training records, disciplinary events
//   - Regional HQ (currently HQ is 100% US)
//
// Needs an ingested shop list — each retail employee is bound to a
// specific shop via home_shop_id, and start_date must be ≥ shop.opened.
package hr

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"emporium/internal/geography"
	"emporium/internal/names"
	"emporium/internal/policy"
	"emporium/internal/rng"
	"emporium/internal/shops"
)

// Person mirrors hr.persons + embeds the current employment_spell
// and current staff_address for JSON-skeleton emission. The DB writer
// splits these (and the V1.6.0 contracts/shifts/payroll-lines
// collections) into their respective target tables.
type Person struct {
	PersonID           int64           `json:"person_id"`
	FirstName          string          `json:"first_name"`
	LastName           string          `json:"last_name"`
	DateOfBirth        string          `json:"date_of_birth"`
	CountryOfResidence string          `json:"country_of_residence"`
	CreatedAt          string          `json:"created_at"`
	SourceSystem       string          `json:"source_system"`
	Spell              EmploymentSpell `json:"employment_spell"`
	Address            StaffAddress    `json:"home_address"`

	// HR operations collections (V1.6.0).
	Contracts    []Contract    `json:"contracts,omitempty"`
	Shifts       []StaffShift  `json:"shifts,omitempty"`
	PayrollLines []PayrollLine `json:"payroll_lines,omitempty"`

	// V1.15.0 — wage history applied after closure post-pass.
	Compensation []CompensationRecord `json:"compensation,omitempty"`
}

// EmploymentSpell mirrors hr.employment_spells. One open spell per
// person in V1 skeleton.
type EmploymentSpell struct {
	SpellID           int64   `json:"spell_id"`
	RoleID            int     `json:"role_id"`
	DepartmentID      int     `json:"department_id"`
	PayGradeID        int     `json:"pay_grade_id"`
	HomeShopID        *int64  `json:"home_shop_id"`
	StartedAt         string  `json:"started_at"`
	EndedAt           *string `json:"ended_at"`
	TerminationReason *string `json:"termination_reason"`
	SourceSystem      string  `json:"source_system"`
}

// StaffAddress mirrors hr.staff_addresses.
type StaffAddress struct {
	StaffAddressID int64   `json:"staff_address_id"`
	EffectiveFrom  string  `json:"effective_from"`
	EffectiveTo    *string `json:"effective_to"`
	Line1          string  `json:"line1"`
	City           string  `json:"city"`
	Region         string  `json:"region"`
	PostalCode     string  `json:"postal_code"`
	CountryCode    string  `json:"country_code"`
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	AnonymisedAt   *string `json:"anonymised_at"`
}

// Generate emits people with embedded current spell + home address.
// Deterministic given (tier, seed, asOf, postals, shopList). The
// payrollRuns argument carries the result of GeneratePayrollRuns —
// passed in so payroll_lines can FK into it. Pass nil to skip
// payroll_line generation entirely.
//
// V1.15.0: hire-date windows are bounded by BusinessClosureDate
// (2016-09-30). Even if the build's asOf is post-closure (e.g.
// 2025-04-23, the archive snapshot date), NO ONE is hired after the
// company is liquidated. Post-generation, applyClosurePostPass sets
// ended_at + termination_reason on every spell.
func Generate(
	tier string,
	seed uint64,
	asOf time.Time,
	postals *geography.Index,
	shopList []shops.Shop,
	payrollRuns []PayrollRun,
	emit func(Person),
) (int, error) {
	if postals.CountFor("US") == 0 {
		return 0, fmt.Errorf("no US postal-code data (needed for HQ staff)")
	}

	// V1.15.0 — hire date sampling stops at BusinessClosureDate even
	// if asOf is later. The build's asOf represents the snapshot date
	// of the post-liquidation archive, not the date the business was
	// operating.
	effectiveAsOf := asOf
	if effectiveAsOf.After(policy.BusinessClosureDate) {
		effectiveAsOf = policy.BusinessClosureDate
	}

	// V1.9.0: per-shop headcount is age-driven (HeadcountForShop) rather
	// than uniform PerShopHeadcount. First-pass compute headcounts so we
	// can size HQ proportionally to the actual retail base, then iterate
	// to emit staff.
	hcRNG := rng.Derive(seed, "hr/headcount")
	shopHeadcounts := make(map[int64]int, len(shopList))
	retailTotal := 0
	for _, s := range shopList {
		shopOpen, err := time.Parse("2006-01-02", s.OpenedDate)
		if err != nil {
			return 0, fmt.Errorf("bad opened_date on shop %d: %w", s.ShopID, err)
		}
		// Use shop's closed_date if present (always true post-V1.15.0),
		// else effectiveAsOf, when sizing headcount.
		shopAsOf := effectiveAsOf
		if s.ClosedDate != nil {
			if cd, err := time.Parse("2006-01-02", *s.ClosedDate); err == nil {
				shopAsOf = cd
			}
		}
		n := policy.HeadcountForShop(shopOpen, shopAsOf, hcRNG)
		shopHeadcounts[s.ShopID] = n
		retailTotal += n
	}
	hqTotal := int(float64(retailTotal) * policy.HQStaffRatio)

	personID := int64(1)
	spellID := int64(1)
	addressID := int64(1)
	var ctr hrCounters
	runsByCountry := PayrollRunsByCountry(payrollRuns)

	// V1.15.0 — wrap caller's emit to apply closure post-pass and
	// compensation history per person. Shop lookup is built up-front.
	shopByID := make(map[int64]shops.Shop, len(shopList))
	for _, s := range shopList {
		shopByID[s.ShopID] = s
	}
	var historyID int64
	// V1.17.0: shift/payroll rows are generated BEFORE applyClosure
	// decides each spell's real end date (most retail staff turn over
	// long before their shop closes). Pre-V1.17 that ordering leaked
	// ~55% ghost shifts and ~76% ghost payroll lines — rows dated after
	// the employee left (training-catalog audit finding F7). Trim them
	// here, after the termination date is known.
	runStart := make(map[int64]time.Time, len(payrollRuns))
	for _, pr := range payrollRuns {
		if t, err := time.Parse("2006-01-02", pr.PeriodStart); err == nil {
			runStart[pr.PayrollRunID] = t
		}
	}
	wrappedEmit := func(p Person) {
		applyClosure(&p, shopByID, seed)
		trimOpsToTermination(&p, runStart)
		p.Compensation = generateCompensation(p, seed, &historyID)
		emit(p)
	}

	// --- Founders (V1.16.2 lore) ---
	// Emitted first so they occupy person_id 1 (Nick Reeves, founder &
	// CEO) and 2 (Jean Macy, his grandmother & co-founder). Their
	// employment_end + termination_reason are hand-set, so the closure
	// post-pass skips them (applyClosure no-ops when EndedAt is set).
	emitFounders(seed, postals, &personID, &spellID, &addressID, &ctr, runsByCountry, wrappedEmit)

	// --- Named executive cast (V1.23.0 lore §3) ---
	// Emitted right after the founders so they take person_id 3-9. Their
	// tenures are hand-set, so wrappedEmit's applyClosure skips them and
	// generateCompensation builds their exec wage histories.
	emitExecs(seed, postals, &personID, &spellID, &addressID, &ctr, runsByCountry, wrappedEmit)

	// --- The fraud pocket's culprit (V1.24.0) ---
	// Seeded immediately after the execs so person_id/spell_id == 10 ==
	// policy.FraudManagerSpellID, which the transactions generator stamps
	// on his keyed refunds. See emitFraudManager / Exhibit H.
	// Canon-only: on the non-canon small tiers this staff spell is not
	// emitted, and the transactions generator likewise skips the fraud
	// pocket (both gated on policy.CanonAnomalies) so no keyed refund ever
	// references a missing spell.
	fraudManagerN := 0
	if policy.CanonAnomalies(tier) {
		emitFraudManager(seed, postals, shopList, &personID, &spellID, &addressID, &ctr, runsByCountry, wrappedEmit)
		fraudManagerN = fraudManagerCount
	}

	// --- Retail staff ---
	for _, shop := range shopList {
		shopOpen, err := time.Parse("2006-01-02", shop.OpenedDate)
		if err != nil {
			return 0, fmt.Errorf("bad opened_date on shop %d: %w", shop.ShopID, err)
		}
		// V1.15.0: bound the hire window by the shop's closed_date (or
		// effectiveAsOf if the shop never closed in the data).
		shopWindow := effectiveAsOf
		if shop.ClosedDate != nil {
			if cd, err := time.Parse("2006-01-02", *shop.ClosedDate); err == nil {
				shopWindow = cd
			}
		}
		n, err := emitRetailStaff(seed, shopWindow, postals, &shop, shopOpen, shopHeadcounts[shop.ShopID],
			&personID, &spellID, &addressID, &ctr, runsByCountry, wrappedEmit)
		if err != nil {
			return 0, err
		}
		_ = n
	}

	// --- HQ staff (US-based) ---
	if hqTotal > 0 {
		// V1.16.4: HQ headcount must track the store network over time,
		// not exist at full strength from 1986. Build a sorted slice of
		// shop open dates; emitHQStaff anchors each hire to a randomly
		// sampled shop's opening, so HQ grows in proportion to the
		// estate (few HQ in the startup years, building out through the
		// 1999-2010 expansion). Fixes the §10.34 early-year P&L
		// distortion (950 HQ vs 24 retail staff in 1990).
		shopOpenDates := make([]time.Time, 0, len(shopList))
		for _, s := range shopList {
			if d, derr := time.Parse("2006-01-02", s.OpenedDate); derr == nil {
				shopOpenDates = append(shopOpenDates, d)
			}
		}
		sort.Slice(shopOpenDates, func(i, j int) bool { return shopOpenDates[i].Before(shopOpenDates[j]) })

		err := emitHQStaff(seed, effectiveAsOf, postals, hqTotal, shopOpenDates,
			&personID, &spellID, &addressID, &ctr, runsByCountry, wrappedEmit)
		if err != nil {
			return 0, err
		}
	}

	// --- Senior leadership cohort (V1.9.0) ---
	// V1.7/V1.8 left pay grades G8/G9/G10 (Senior Director, VP,
	// Executive) completely empty. A 7,000-shop multinational with
	// $10B+ revenue needs senior leadership for top-pay queries to
	// return any rows. Emit a small fixed cohort here regardless of
	// tier size — these numbers are realistic absolute counts for a
	// company of NGE's scale (~3 C-suite, ~10 VPs, ~30 Sr Directors).
	hqShopOpen := policy.USFirstEraStart
	seniorTotal, err := emitSeniorLeaders(seed, effectiveAsOf, postals, hqShopOpen, len(shopList),
		&personID, &spellID, &addressID, &ctr, runsByCountry, wrappedEmit)
	if err != nil {
		return 0, err
	}

	const founderCount = 2 // Nick Reeves + Jean Macy
	return retailTotal + hqTotal + seniorTotal + founderCount + execCount + fraudManagerN, nil
}

// emitFounders emits the two lore characters at the top of NGE's org
// chart, occupying person_id 1 and 2:
//
//   1. Nick Reeves — founder & CEO. Hired on the founding date
//      (1986-08-06), stays through to the 2016-09-30 liquidation
//      (goes down with the ship). Pay grade 10.
//   2. Jean Macy — Nick's grandmother & co-founder, who put up the
//      seed money for the first Chicago store. Retires end of 1996
//      (age ~71). Pay grade 10.
//
// Both are US/Chicago-based. Their EndedAt + TerminationReason are
// set here so applyClosure skips them; generateCompensation still
// runs (via wrappedEmit) to build their wage histories.
func emitFounders(
	seed uint64,
	postals *geography.Index,
	personID, spellID, addressID *int64,
	ctr *hrCounters,
	runsByCountry map[string][]PayrollRun,
	emit func(Person),
) {
	opsRNG := rng.Derive(seed, "hr/founders/ops")
	postalRNG := rng.Derive(seed, "hr/founders/postal")
	streetRNG := rng.Derive(seed, "hr/founders/street")

	chicago := func() (line1, city, region, postal string, lat, lon float64) {
		pc, ok := postals.SampleByCity("US", "Chicago", "IL", postalRNG) // V1.23.0: AdminCode1 is "IL", not "Illinois" — the full name never matched, silently exiling HQ staff to random cities
		if !ok {
			pc, _ = postals.Sample("US", postalRNG)
		}
		return geography.GenerateStreetAddress("US", pc.City, streetRNG),
			pc.City, pc.Region, pc.Postal, pc.Latitude, pc.Longitude
	}

	type founder struct {
		first, last string
		dob         string
		started     string
		endedAt     string
		termReason  string
		roleID      int
	}
	founders := []founder{
		{"Nick", "Reeves", "1956-03-12", "1986-08-06", "2016-09-30", "liquidation_2016", 19},
		{"Jean", "Macy", "1925-06-30", "1986-08-06", "1996-12-31", "retirement", 20},
	}

	for _, f := range founders {
		role := policy.RoleByID(f.roleID)
		line1, city, region, postal, lat, lon := chicago()
		endedAt := f.endedAt
		termReason := f.termReason
		spell := EmploymentSpell{
			SpellID:           *spellID,
			RoleID:            role.ID,
			DepartmentID:      role.DepartmentID,
			PayGradeID:        10, // founding family at the top grade
			HomeShopID:        nil,
			StartedAt:         f.started,
			EndedAt:           &endedAt,
			TerminationReason: &termReason,
			SourceSystem:      policy.HRSourceSystemForYear(1986),
		}
		addr := StaffAddress{
			StaffAddressID: *addressID,
			EffectiveFrom:  f.started,
			Line1:          line1,
			City:           city,
			Region:         region,
			PostalCode:     postal,
			CountryCode:    "US",
			Latitude:       lat,
			Longitude:      lon,
		}
		p := Person{
			PersonID:           *personID,
			FirstName:          f.first,
			LastName:           f.last,
			DateOfBirth:        f.dob,
			CountryOfResidence: "US",
			CreatedAt:          f.started + "T00:00:00Z",
			SourceSystem:       policy.HRSourceSystemForYear(1986),
			Spell:              spell,
			Address:            addr,
		}
		p.Contracts = generateContracts(&spell, ctr, opsRNG)
		hours := 50.0 // founders work long hours
		if len(p.Contracts) > 0 && p.Contracts[0].WeeklyHours != nil {
			hours = *p.Contracts[0].WeeklyHours
		}
		p.PayrollLines = generatePayrollLines(&spell, hours, "US",
			runsByCountry["US"], ctr, opsRNG)
		emit(p)

		*personID++
		*spellID++
		*addressID++
	}
}

// execCount is the number of named hired executives seeded by emitExecs.
// Kept in sync with the founders count so estimated headcount matches
// the actual emit.
const execCount = 7 // Harrington, Voss, Wickersham, Rossiter, Pyle, Blaine-Hurst, Macy(Todd)

// emitExecs seeds the named hired-executive cast (lore §3) immediately
// after the founders, occupying person_id 3-9. Unlike the founders (owner-
// operators on grade-10 founder roles), these are hired guns on the V1.23.0
// exec roles (21-24) — so they draw full grade-scaled executive comp and,
// in profitable years, the grade-scaled annual bonus (which is why Brock
// Rossiter's peak-2007 bonus lands in an otherwise loss-adjacent arc).
//
// Their tenures are hand-set (StartedAt/EndedAt/TerminationReason), so the
// closure post-pass skips them and the sequential CEO succession
// (Harrington → Rossiter → Pyle → Blaine-Hurst) reads straight out of
// hr.employment_spells. All are US/Chicago-based (HQ, Northern District of
// Illinois). Morty Fein is deliberately NOT here — he was an investor and
// board figure, never on payroll.
func emitExecs(
	seed uint64,
	postals *geography.Index,
	personID, spellID, addressID *int64,
	ctr *hrCounters,
	runsByCountry map[string][]PayrollRun,
	emit func(Person),
) {
	opsRNG := rng.Derive(seed, "hr/execs/ops")
	postalRNG := rng.Derive(seed, "hr/execs/postal")
	streetRNG := rng.Derive(seed, "hr/execs/street")

	chicago := func() (line1, city, region, postal string, lat, lon float64) {
		pc, ok := postals.SampleByCity("US", "Chicago", "IL", postalRNG) // V1.23.0: AdminCode1 is "IL", not "Illinois" — the full name never matched, silently exiling HQ staff to random cities
		if !ok {
			pc, _ = postals.Sample("US", postalRNG)
		}
		return geography.GenerateStreetAddress("US", pc.City, streetRNG),
			pc.City, pc.Region, pc.Postal, pc.Latitude, pc.Longitude
	}

	type exec struct {
		first, last string
		dob         string
		started     string
		endedAt     string
		termReason  string
		roleID      int
	}
	// Dates trace the lore's C-suite carousel: overlapping CFO/EVP tenures
	// and a clean sequential CEO handoff with no gaps.
	execs := []exec{
		{"Chip", "Harrington", "1955-11-02", "1996-06-01", "2003-09-30", "resignation", 21},          // COO→CEO, ousted w/ parachute
		{"Marlene", "Voss", "1952-04-18", "1997-01-06", "2003-06-30", "termination_for_cause", 22},   // CFO, phantom payroll
		{"Dale", "Wickersham", "1948-09-30", "1999-02-01", "2011-06-30", "retirement", 23},            // EVP Pre-Owned & Trade
		{"Brock", "Rossiter", "1962-07-14", "2003-10-01", "2009-03-31", "resignation", 21},            // CEO, the supercycle
		{"Gordon", "Pyle", "1951-01-23", "2009-04-01", "2014-11-30", "resignation", 21},               // CEO, the digital denier
		{"Priscilla", "Blaine-Hurst", "1968-05-09", "2014-12-01", "2016-09-30", "liquidation_2016", 21}, // CEO through Chapter 11
		{"Todd", "Macy", "1984-03-22", "1998-09-07", "2016-09-30", "liquidation_2016", 24},            // webmaster, the nephew
	}
	// V1.27: person_id 1-9 is a DELIBERATE leadership roster — the founders
	// (1-2) then the named C-suite cast, keyed into the HRIS ahead of the
	// rank-and-file (Exhibit F, "The Succession", reads them as person_id
	// 1-9; the fraud manager sits at 10). It is intentionally NOT a hire-date
	// ordering across the whole company. Within the block, though, sort the
	// cast by start date so the roster itself reads chronologically (the
	// webmaster, hired 1998, no longer trails the 1999/2003 execs).
	sort.Slice(execs, func(i, j int) bool { return execs[i].started < execs[j].started })

	for _, e := range execs {
		role := policy.RoleByID(e.roleID)
		line1, city, region, postal, lat, lon := chicago()
		endedAt := e.endedAt
		termReason := e.termReason
		spell := EmploymentSpell{
			SpellID:           *spellID,
			RoleID:            role.ID,
			DepartmentID:      role.DepartmentID,
			PayGradeID:        role.DefaultGradeID, // exec grade drives comp
			HomeShopID:        nil,                 // corporate — no home shop
			StartedAt:         e.started,
			EndedAt:           &endedAt,
			TerminationReason: &termReason,
			SourceSystem:      policy.HRSourceSystemForYear(yearOf(e.started)),
		}
		addr := StaffAddress{
			StaffAddressID: *addressID,
			EffectiveFrom:  e.started,
			Line1:          line1,
			City:           city,
			Region:         region,
			PostalCode:     postal,
			CountryCode:    "US",
			Latitude:       lat,
			Longitude:      lon,
		}
		p := Person{
			PersonID:           *personID,
			FirstName:          e.first,
			LastName:           e.last,
			DateOfBirth:        e.dob,
			CountryOfResidence: "US",
			CreatedAt:          e.started + "T00:00:00Z",
			SourceSystem:       policy.HRSourceSystemForYear(yearOf(e.started)),
			Spell:              spell,
			Address:            addr,
		}
		p.Contracts = generateContracts(&spell, ctr, opsRNG)
		hours := 50.0
		if e.roleID == 24 {
			hours = 40.0 // the webmaster is not, whatever else, an executive
		}
		if len(p.Contracts) > 0 && p.Contracts[0].WeeklyHours != nil {
			hours = *p.Contracts[0].WeeklyHours
		}
		p.PayrollLines = generatePayrollLines(&spell, hours, "US",
			runsByCountry["US"], ctr, opsRNG)
		emit(p)

		*personID++
		*spellID++
		*addressID++
	}
}

// yearOf extracts the year from a YYYY-MM-DD string, defaulting to 1986 on
// a parse failure (the seeded exec dates are all well-formed).
func yearOf(ymd string) int {
	if t, err := time.Parse("2006-01-02", ymd); err == nil {
		return t.Year()
	}
	return 1986
}

// fraudManagerCount is the number of persons emitFraudManager seeds.
const fraudManagerCount = 1

// emitFraudManager seeds the V1.24.0 fraud pocket's culprit as
// person_id 10 / spell_id 10 (policy.FraudManagerSpellID): Marcus
// Freel, Store Manager of the fraud shop (policy.FraudShopCode,
// resolved to a shop_id via the shop list) from 2003 until a quiet
// 2009 resignation. A perfectly ordinary retail manager record — the
// ONLY thing anomalous about him is what the transactions at his shop
// did between 2006-01 and 2007-06, and the spell_id his keyed refunds
// carry. He was never caught. The archive is the first to know.
//
// His HomeShopID is set (retail, not corporate), so he draws normal
// store-manager pay — embezzlers look ordinary in the comp table;
// that's rather the point.
func emitFraudManager(
	seed uint64,
	postals *geography.Index,
	shopList []shops.Shop,
	personID, spellID, addressID *int64,
	ctr *hrCounters,
	runsByCountry map[string][]PayrollRun,
	emit func(Person),
) {
	var fraudShop *shops.Shop
	for i := range shopList {
		if shopList[i].ShopCode == policy.FraudShopCode {
			fraudShop = &shopList[i]
			break
		}
	}
	if fraudShop == nil {
		return // shop code absent (shouldn't happen at any tier)
	}

	opsRNG := rng.Derive(seed, "hr/fraud_manager/ops")
	postalRNG := rng.Derive(seed, "hr/fraud_manager/postal")
	streetRNG := rng.Derive(seed, "hr/fraud_manager/street")

	pc, ok := postals.SampleByCityRegionName(fraudShop.CountryCode, fraudShop.Address.City, fraudShop.Address.Region, postalRNG)
	if !ok {
		pc, _ = postals.Sample(fraudShop.CountryCode, postalRNG)
	}

	role := policy.RoleByID(3) // Store Manager
	started := "2003-04-14"
	endedAt := "2009-08-31"
	termReason := "resignation"
	homeShop := fraudShop.ShopID
	spell := EmploymentSpell{
		SpellID:           *spellID,
		RoleID:            role.ID,
		DepartmentID:      role.DepartmentID,
		PayGradeID:        role.DefaultGradeID,
		HomeShopID:        &homeShop,
		StartedAt:         started,
		EndedAt:           &endedAt,
		TerminationReason: &termReason,
		SourceSystem:      policy.HRSourceSystemForYear(2003),
	}
	addr := StaffAddress{
		StaffAddressID: *addressID,
		EffectiveFrom:  started,
		Line1:          geography.GenerateStreetAddress(fraudShop.CountryCode, pc.City, streetRNG),
		City:           pc.City,
		Region:         pc.Region,
		PostalCode:     pc.Postal,
		CountryCode:    fraudShop.CountryCode,
		Latitude:       pc.Latitude,
		Longitude:      pc.Longitude,
	}
	p := Person{
		PersonID:           *personID,
		FirstName:          "Marcus",
		LastName:           "Freel",
		DateOfBirth:        "1971-02-11",
		CountryOfResidence: fraudShop.CountryCode,
		CreatedAt:          started + "T00:00:00Z",
		SourceSystem:       policy.HRSourceSystemForYear(2003),
		Spell:              spell,
		Address:            addr,
	}
	p.Contracts = generateContracts(&spell, ctr, opsRNG)
	hours := 40.0
	if len(p.Contracts) > 0 && p.Contracts[0].WeeklyHours != nil {
		hours = *p.Contracts[0].WeeklyHours
	}
	p.PayrollLines = generatePayrollLines(&spell, hours, fraudShop.CountryCode,
		runsByCountry[fraudShop.CountryCode], ctr, opsRNG)
	emit(p)

	*personID++
	*spellID++
	*addressID++
}

func emitRetailStaff(
	seed uint64,
	asOf time.Time,
	postals *geography.Index,
	shop *shops.Shop,
	shopOpen time.Time,
	headcount int,
	personID, spellID, addressID *int64,
	ctr *hrCounters,
	runsByCountry map[string][]PayrollRun,
	emit func(Person),
) (int, error) {
	ns := fmt.Sprintf("hr/shop/%d", shop.ShopID)
	nameRNG := rng.Derive(seed, ns+"/name")
	startRNG := rng.Derive(seed, ns+"/start")
	dobRNG := rng.Derive(seed, ns+"/dob")
	postalRNG := rng.Derive(seed, ns+"/postal")
	streetRNG := rng.Derive(seed, ns+"/street")
	opsRNG := rng.Derive(seed, ns+"/ops")

	homeShopID := shop.ShopID
	span := asOf.Sub(shopOpen)
	if span < 0 {
		return 0, fmt.Errorf("shop %d opens after asOf", shop.ShopID)
	}

	// V1.9.0: cluster the first 3 hires within ~6 months of shopOpen
	// (founding crew — Nick can't run the shop alone) and spread the
	// remainder uniformly across the rest of the shop's lifespan.
	foundingCount := 3
	if foundingCount > headcount {
		foundingCount = headcount
	}
	foundingSpan := 180 * 24 * time.Hour // 6 months
	if span < foundingSpan {
		foundingSpan = span
	}
	startDates := make([]time.Time, headcount)
	for i := 0; i < foundingCount; i++ {
		startDates[i] = shopOpen.Add(time.Duration(startRNG.Float64() * float64(foundingSpan)))
	}
	if headcount > foundingCount {
		lateStart := shopOpen.Add(foundingSpan)
		lateSpan := asOf.Sub(lateStart)
		if lateSpan < 0 {
			lateSpan = 0
		}
		for i := foundingCount; i < headcount; i++ {
			startDates[i] = lateStart.Add(time.Duration(startRNG.Float64() * float64(lateSpan)))
		}
	}

	for i := 0; i < headcount; i++ {
		// Slot 0 = Store Manager, slot 1 = Assistant Manager, rest = SA.
		// Larger shops get more management depth: 2nd AM at headcount ≥ 12,
		// 3rd AM at headcount ≥ 18.
		var roleID int
		switch {
		case i == 0:
			roleID = 3 // Store Manager
		case i == 1:
			roleID = 2 // Assistant Manager
		case i == 2 && headcount >= 12:
			roleID = 2 // 2nd Assistant Manager (busy shops)
		case i == 3 && headcount >= 18:
			roleID = 2 // 3rd Assistant Manager (flagship shops)
		default:
			roleID = 1 // Sales Associate
		}
		role := policy.RoleByID(roleID)

		first, last := names.Pick(shop.CountryCode, nameRNG)
		started := startDates[i]
		dob := sampleDOB(started, dobRNG)

		pc, _ := postals.Sample(shop.CountryCode, postalRNG)
		line1 := geography.GenerateStreetAddress(shop.CountryCode, pc.City, streetRNG)

		spell := EmploymentSpell{
			SpellID:      *spellID,
			RoleID:       role.ID,
			DepartmentID: role.DepartmentID,
			PayGradeID:   role.DefaultGradeID,
			HomeShopID:   &homeShopID,
			StartedAt:    started.Format("2006-01-02"),
			SourceSystem: policy.HRSourceSystemForYear(started.Year()),
		}
		addr := StaffAddress{
			StaffAddressID: *addressID,
			EffectiveFrom:  started.Format("2006-01-02"),
			Line1:          line1,
			City:           pc.City,
			Region:         pc.Region,
			PostalCode:     pc.Postal,
			CountryCode:    shop.CountryCode,
			Latitude:       pc.Latitude,
			Longitude:      pc.Longitude,
		}
		p := Person{
			PersonID:           *personID,
			FirstName:          first,
			LastName:           last,
			DateOfBirth:        dob.Format("2006-01-02"),
			CountryOfResidence: shop.CountryCode,
			CreatedAt:          started.Format("2006-01-02T15:04:05Z"),
			SourceSystem:       policy.HRSourceSystemForYear(started.Year()),
			Spell:              spell,
			Address:            addr,
		}
		// Operations collections (V1.6.0).
		p.Contracts = generateContracts(&spell, ctr, opsRNG)
		hours := 35.0
		if len(p.Contracts) > 0 && p.Contracts[0].WeeklyHours != nil {
			hours = *p.Contracts[0].WeeklyHours
		}
		p.Shifts = generateShifts(&spell, hours, asOf, ctr, opsRNG)
		p.PayrollLines = generatePayrollLines(&spell, hours, shop.CountryCode,
			runsByCountry[shop.CountryCode], ctr, opsRNG)
		emit(p)

		*personID++
		*spellID++
		*addressID++
	}
	return headcount, nil
}

func emitHQStaff(
	seed uint64,
	asOf time.Time,
	postals *geography.Index,
	n int,
	shopOpenDates []time.Time,
	personID, spellID, addressID *int64,
	ctr *hrCounters,
	runsByCountry map[string][]PayrollRun,
	emit func(Person),
) error {
	ns := "hr/hq"
	nameRNG := rng.Derive(seed, ns+"/name")
	startRNG := rng.Derive(seed, ns+"/start")
	dobRNG := rng.Derive(seed, ns+"/dob")
	postalRNG := rng.Derive(seed, ns+"/postal")
	streetRNG := rng.Derive(seed, ns+"/street")
	roleRNG := rng.Derive(seed, ns+"/role")
	opsRNG := rng.Derive(seed, ns+"/ops")

	// V1.16.4: HQ start dates track the store-network growth curve.
	// Each HQ hire is anchored to a randomly-sampled shop's open date
	// (so the more shops opening in an era, the more HQ hired then),
	// plus a forward jitter of up to 1 year (HQ builds out slightly
	// after the stores it supports). Falls back to a uniform spread if
	// no shop dates were supplied. This keeps early-year HQ headcount
	// proportional to the tiny startup estate instead of full-strength
	// from 1986.
	earliestStart := policy.USFirstEraStart
	if len(shopOpenDates) > 0 {
		earliestStart = shopOpenDates[0]
	}
	uniformSpan := asOf.Sub(earliestStart)
	const hqJitter = 365 * 24 * time.Hour // HQ lags store openings by up to 1y

	for i := 0; i < n; i++ {
		first, last := names.Pick("US", nameRNG)
		var started time.Time
		if len(shopOpenDates) > 0 {
			anchor := shopOpenDates[startRNG.IntN(len(shopOpenDates))]
			started = anchor.Add(time.Duration(startRNG.Float64() * float64(hqJitter)))
			if started.After(asOf) {
				started = asOf
			}
		} else {
			started = earliestStart.Add(time.Duration(startRNG.Float64() * float64(uniformSpan)))
		}
		// V1.9.0: era-gate HQ role pool. A 1986 HQ has no Software
		// Engineers; engineering / digital-marketing roles emerge over
		// the simulator's timespan per HQRoleWeightsForYear.
		roleID := sampleHQRoleForYear(started.Year(), roleRNG)
		role := policy.RoleByID(roleID)
		dob := sampleDOB(started, dobRNG)

		pc, _ := postals.Sample("US", postalRNG)
		line1 := geography.GenerateStreetAddress("US", pc.City, streetRNG)

		spell := EmploymentSpell{
			SpellID:      *spellID,
			RoleID:       role.ID,
			DepartmentID: role.DepartmentID,
			PayGradeID:   role.DefaultGradeID,
			HomeShopID:   nil, // HQ staff: no home shop
			StartedAt:    started.Format("2006-01-02"),
			SourceSystem: policy.HRSourceSystemForYear(started.Year()),
		}
		addr := StaffAddress{
			StaffAddressID: *addressID,
			EffectiveFrom:  started.Format("2006-01-02"),
			Line1:          line1,
			City:           pc.City,
			Region:         pc.Region,
			PostalCode:     pc.Postal,
			CountryCode:    "US",
			Latitude:       pc.Latitude,
			Longitude:      pc.Longitude,
		}
		p := Person{
			PersonID:           *personID,
			FirstName:          first,
			LastName:           last,
			DateOfBirth:        dob.Format("2006-01-02"),
			CountryOfResidence: "US",
			CreatedAt:          started.Format("2006-01-02T15:04:05Z"),
			SourceSystem:       policy.HRSourceSystemForYear(started.Year()),
			Spell:              spell,
			Address:            addr,
		}
		// HQ ops: contract + payroll lines, no shifts (HQ staff don't
		// punch shop shifts).
		p.Contracts = generateContracts(&spell, ctr, opsRNG)
		hours := 40.0
		if len(p.Contracts) > 0 && p.Contracts[0].WeeklyHours != nil {
			hours = *p.Contracts[0].WeeklyHours
		}
		p.PayrollLines = generatePayrollLines(&spell, hours, "US",
			runsByCountry["US"], ctr, opsRNG)
		emit(p)

		*personID++
		*spellID++
		*addressID++
	}
	return nil
}

// sampleHQRoleForYear picks a non-retail role weighted by
// policy.HQRoleWeightsForYear(year). V1.9.0 replaces the era-blind
// sampleHQRole — engineering/IT roles only appear post-1994, modern
// HQ mix from 2015+.
func sampleHQRoleForYear(year int, r *rand.Rand) int {
	weights := policy.HQRoleWeightsForYear(year)
	var totalW float64
	for _, w := range weights {
		totalW += w.Weight
	}
	u := r.Float64() * totalW
	cum := 0.0
	for _, w := range weights {
		cum += w.Weight
		if u <= cum {
			return w.RoleID
		}
	}
	return weights[len(weights)-1].RoleID
}

// emitSeniorLeaders generates a small fixed cohort of senior leaders
// (V1.9.0) at pay grades G8/G9/G10 (Senior Director, VP, Executive).
// These are US-based, drawn from the existing senior role IDs (Finance
// Manager, Engineering Manager, Marketing Manager, Senior Buyer, HR
// Business Partner), with the pay grade overridden to the senior tier
// regardless of the role's default grade. Headcount is fixed at ~43
// — realistic absolute counts for a 7,000-shop multinational.
//
// Start dates are deliberately spread across NGE's history with a
// skew toward earlier years (senior leadership tends to be long-
// tenure). Hours are 40-50/week.
func emitSeniorLeaders(
	seed uint64,
	asOf time.Time,
	postals *geography.Index,
	earliestStart time.Time,
	totalShops int,
	personID, spellID, addressID *int64,
	ctr *hrCounters,
	runsByCountry map[string][]PayrollRun,
	emit func(Person),
) (int, error) {
	ns := "hr/senior"
	nameRNG := rng.Derive(seed, ns+"/name")
	startRNG := rng.Derive(seed, ns+"/start")
	dobRNG := rng.Derive(seed, ns+"/dob")
	postalRNG := rng.Derive(seed, ns+"/postal")
	streetRNG := rng.Derive(seed, ns+"/street")
	roleRNG := rng.Derive(seed, ns+"/role")
	opsRNG := rng.Derive(seed, ns+"/ops")

	// V1.16.2: scale the senior-leadership cohort by tier so smaller
	// tiers aren't top-heavy. The baseline cohort (30/10/3 = 43) is
	// calibrated for the 7,000-shop 3T tier; scale sub-linearly by shop
	// count via (shops/7000)^0.3, with per-grade floors so even the 3g
	// tier keeps a plausible minimal C-suite.
	//   3g (10)   → ~5   (SD 3, VP 1, Exec 1)
	//   30g (65)  → ~10  (SD 7, VP 2, Exec 1)
	//   300g (700)→ ~22  (SD 15, VP 5, Exec 2)
	//   3T (7000) → 43   (SD 30, VP 10, Exec 3)
	shopScale := math.Pow(float64(totalShops)/7000.0, 0.3)
	if shopScale > 1.0 {
		shopScale = 1.0
	}
	scaled := func(base, floor int) int {
		n := int(math.Round(float64(base) * shopScale))
		if n < floor {
			n = floor
		}
		return n
	}

	// Cohort: (count, pay_grade, eligible_role_ids).
	cohort := []struct {
		count   int
		gradeID int
		roles   []int
	}{
		{scaled(30, 2), 8, []int{4, 5, 6, 10, 12, 14, 16, 18}}, // Senior Directors — broad
		{scaled(10, 1), 9, []int{5, 10, 12, 14}},               // VPs — finance/eng/marketing/buying
		{scaled(3, 1), 10, []int{5, 10}},                        // Executive — Fin Mgr (CFO) or Eng Mgr (CTO/COO)
	}

	// V1.16.2: senior leadership ramps WITH company growth instead of
	// clustering in the startup years. A 1-shop 1986 NGE doesn't have
	// 30 senior directors; the C-suite fills out through the 1990s-2000s
	// expansion. Start dates are distributed across the 1986→2007-peak
	// growth window with a concave curve biased toward the later
	// (mature) years, and capped at the peak — no net senior-leadership
	// additions during the 2008-2016 decline. This removes the V1.16.1
	// early-year P&L distortion (huge wage bill vs near-zero startup
	// revenue) flagged in §10.34.
	growthSpan := policy.BusinessPeakDate.Sub(earliestStart)
	if growthSpan <= 0 {
		growthSpan = asOf.Sub(earliestStart)
	}
	total := 0
	for _, group := range cohort {
		for i := 0; i < group.count; i++ {
			roleID := group.roles[roleRNG.IntN(len(group.roles))]
			role := policy.RoleByID(roleID)

			first, last := names.Pick("US", nameRNG)
			// Concave ramp: 1-(1-u)^2 biases toward the later growth
			// years (most senior hires land in the 1990s-2000s build-out),
			// capped at the 2007 peak.
			u := startRNG.Float64()
			u = 1 - (1-u)*(1-u) // bias toward later (growth → peak)
			started := earliestStart.Add(time.Duration(u * float64(growthSpan)))
			dob := sampleDOB(started, dobRNG)

			pc, _ := postals.Sample("US", postalRNG)
			line1 := geography.GenerateStreetAddress("US", pc.City, streetRNG)

			spell := EmploymentSpell{
				SpellID:      *spellID,
				RoleID:       role.ID,
				DepartmentID: role.DepartmentID,
				PayGradeID:   group.gradeID, // V1.9.0 override
				HomeShopID:   nil,
				StartedAt:    started.Format("2006-01-02"),
				SourceSystem: policy.HRSourceSystemForYear(started.Year()),
			}
			addr := StaffAddress{
				StaffAddressID: *addressID,
				EffectiveFrom:  started.Format("2006-01-02"),
				Line1:          line1,
				City:           pc.City,
				Region:         pc.Region,
				PostalCode:     pc.Postal,
				CountryCode:    "US",
				Latitude:       pc.Latitude,
				Longitude:      pc.Longitude,
			}
			p := Person{
				PersonID:           *personID,
				FirstName:          first,
				LastName:           last,
				DateOfBirth:        dob.Format("2006-01-02"),
				CountryOfResidence: "US",
				CreatedAt:          started.Format("2006-01-02T15:04:05Z"),
				SourceSystem:       policy.HRSourceSystemForYear(started.Year()),
				Spell:              spell,
				Address:            addr,
			}
			p.Contracts = generateContracts(&spell, ctr, opsRNG)
			hours := 45.0 // senior leaders work longer
			if len(p.Contracts) > 0 && p.Contracts[0].WeeklyHours != nil {
				hours = *p.Contracts[0].WeeklyHours
			}
			p.PayrollLines = generatePayrollLines(&spell, hours, "US",
				runsByCountry["US"], ctr, opsRNG)
			emit(p)

			*personID++
			*spellID++
			*addressID++
			total++
		}
	}
	return total, nil
}

// sampleDOB produces a plausible DOB so age-at-start is 18–65,
// median ~32. Normal-ish via Box–Muller.
func sampleDOB(started time.Time, r *rand.Rand) time.Time {
	u1, u2 := r.Float64(), r.Float64()
	if u1 < 1e-9 {
		u1 = 1e-9
	}
	z := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	age := 32.0 + z*10.0
	if age < 18 {
		age = 18
	}
	if age > 65 {
		age = 65
	}
	days := int(age * 365.25)
	return started.AddDate(0, 0, -days)
}
