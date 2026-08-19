// Package shops generates Nick's Gaming Emporium shop records
// deterministically per §9.15.4.
//
// Two-phase expansion model:
//
//   1. **US-first establishment** (1986–1998). The first min(30, total)
//      shops are US-only. Nick proves the chain domestically before
//      going international. At tiers below 30 shops total, the whole
//      estate is US — a snapshot of NGE during its domestic-only era.
//
//   2. **International expansion** (1999–2015). Remaining shops open —
//      additional US growth plus all non-US branches, allocated per
//      §9.15.4's country shares.
//
// Each shop gets a real postal code + city (sampled deterministically
// from seed_data/postal_codes.tsv per §9.12.7) and a synthetic
// locale-aware street address per geography/addresses.go.
//
// Consolidation era (2016+) is net-flat in the skeleton; closure
// modelling comes later.
package shops

import (
	"fmt"
	"math"
	"sort"
	"time"

	"emporium/internal/geography"
	"emporium/internal/policy"
	"emporium/internal/rng"
)

// Shop is the skeleton-level shop record with embedded address.
type Shop struct {
	ShopID        int64       `json:"shop_id"`
	ShopCode      string      `json:"shop_code"`
	Name          string      `json:"name"`
	CountryCode   string      `json:"country_code"`
	CurrencyCode  string      `json:"currency_code"`
	OpenedDate    string      `json:"opened_date"`
	ClosedDate    *string     `json:"closed_date"`
	ClosureReason string      `json:"closure_reason,omitempty"` // V1.15.0
	SourceSystem  string      `json:"source_system"`
	Address       ShopAddress `json:"address"`
}

// ShopAddress mirrors the retail.shop_addresses row that will be split
// off on DB insert. Carried nested on Shop for the JSON skeleton.
type ShopAddress struct {
	ShopAddressID int64   `json:"shop_address_id"`
	Line1         string  `json:"line1"`
	City          string  `json:"city"`
	Region        string  `json:"region"`
	PostalCode    string  `json:"postal_code"`
	CountryCode   string  `json:"country_code"`
	Latitude      float64 `json:"latitude"`
	Longitude     float64 `json:"longitude"`
}

// Generate produces the full shop estate for a given tier as of a given
// date, using the supplied postal-code index to ground each shop in
// real geography.
//
// Deterministic: same (tier, seed, asOf, postal-code TSV) yields
// byte-identical output across runs and machines.
func Generate(tier string, seed uint64, asOf time.Time, postals *geography.Index) ([]Shop, error) {
	total, ok := policy.ShopCountByTier[tier]
	if !ok {
		return nil, fmt.Errorf("unknown tier %q (expected 3g|30g|300g|3t)", tier)
	}

	allocations := allocatePerCountry(total)
	datesByCountry := buildDatePools(seed, total, asOf, allocations)

	shops := make([]Shop, 0, total)
	for _, share := range policy.ShopShares {
		n := allocations[share.Country]
		if n == 0 {
			continue
		}
		if postals.CountFor(share.Country) == 0 {
			return nil, fmt.Errorf("no postal-code data for %s (expected in seed_data/postal_codes.tsv)", share.Country)
		}
		postalRNG := rng.Derive(seed, "shops/postal/"+share.Country)
		streetRNG := rng.Derive(seed, "shops/street/"+share.Country)
		dates := datesByCountry[share.Country]

		for i := 0; i < n; i++ {
			opened := dates[i]
			// V1.13.6: anchor cities get the first slots in country
			// order. Falls back to population-weighted sample if either
			// (a) all anchors are placed, or (b) the i-th anchor has no
			// postal-codes city match in this country.
			var pc geography.PostalCode
			placed := false
			if i < len(share.AnchorCities) {
				anchor := share.AnchorCities[i]
				pc, placed = postals.SampleByCity(share.Country, anchor.City, anchor.Region, postalRNG)
			}
			if !placed {
				pc, _ = postals.Sample(share.Country, postalRNG)
			}
			addr := ShopAddress{
				Line1:       geography.GenerateStreetAddress(share.Country, pc.City, streetRNG),
				City:        pc.City,
				Region:      pc.Region,
				PostalCode:  pc.Postal,
				CountryCode: share.Country,
				Latitude:    pc.Latitude,
				Longitude:   pc.Longitude,
			}
			shops = append(shops, Shop{
				ShopCode:     fmt.Sprintf("%s-%04d", share.Country, i+1),
				Name:         shopNameFor(share.Country, pc.City),
				CountryCode:  share.Country,
				CurrencyCode: share.CurrencyCode,
				OpenedDate:   opened.Format("2006-01-02"),
				ClosedDate:   nil,
				SourceSystem: policy.SourceSystemForYear(opened.Year()),
				Address:      addr,
			})
		}
	}

	// V1.19 — append the early-era churn cohort: transient shops that
	// open AND close within the expansion window (lease_expiry churn).
	// Generated outside the peak estate (fresh RNG namespaces) so the
	// 2010-12-31 count (the EstatePeak narrative divisor, == total) is
	// unchanged. churnCount ≈ EarlyChurnAnnualRate × peak × expansion-yrs.
	churnCount := int(math.Round(policy.EarlyChurnAnnualRate * float64(total) * 12.0))
	transients, err := generateTransientShops(seed, asOf, postals, churnCount)
	if err != nil {
		return nil, err
	}
	shops = append(shops, transients...)

	// Sort so shop_id follows global opened_date order.
	sort.Slice(shops, func(i, j int) bool {
		if shops[i].OpenedDate != shops[j].OpenedDate {
			return shops[i].OpenedDate < shops[j].OpenedDate
		}
		return shops[i].ShopCode < shops[j].ShopCode
	})
	for idx := range shops {
		shops[idx].ShopID = int64(idx + 1)
		shops[idx].Address.ShopAddressID = int64(idx + 1)
	}

	// V1.15.0 — assign each shop a closure date + reason per the
	// 2011-2016 wind-down curve.
	assignClosureSchedule(seed, shops)

	return shops, nil
}

// assignClosureSchedule populates ClosedDate + ClosureReason on every
// shop in the slice per policy.ClosureCurveFractions. By the end of
// the wind-down (2016-09-30) every shop is closed.
//
// The closure permutation is independent of opened_date and country
// (uniform random) — real liquidations are largely driven by lease
// expiries and corporate decisions, not regional patterns. Tier-
// scaling uses largest-remainder rounding so the sum of per-year
// closures equals the shop count exactly.
func assignClosureSchedule(seed uint64, shops []Shop) {
	if len(shops) == 0 {
		return
	}

	// V1.19: shops that already carry a ClosedDate are the transient
	// early-churn cohort (opened AND closed within the expansion window).
	// The 2011-2016 wind-down curve applies ONLY to the surviving peak
	// estate, so collect survivor indices and size the curve to them.
	// This keeps the 2010-12-31 count (== ShopCountByTier) intact.
	survivors := make([]int, 0, len(shops))
	for i := range shops {
		if shops[i].ClosedDate == nil {
			survivors = append(survivors, i)
		}
	}
	if len(survivors) == 0 {
		return
	}

	// Compute per-year close counts via largest-remainder rounding.
	total := len(survivors)
	type entry struct {
		year    int
		ideal   float64
		rounded int
	}
	entries := make([]entry, 0, len(policy.ClosureCurveFractions))
	used := 0
	for _, f := range policy.ClosureCurveFractions {
		ideal := f.Fraction * float64(total)
		r := int(ideal)
		entries = append(entries, entry{year: f.Year, ideal: ideal, rounded: r})
		used += r
	}
	leftover := total - used
	// Distribute leftover to the years with the highest fractional part,
	// preserving year order on ties.
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		fi := entries[order[i]].ideal - float64(entries[order[i]].rounded)
		fj := entries[order[j]].ideal - float64(entries[order[j]].rounded)
		return fi > fj
	})
	for k := 0; k < leftover && k < len(order); k++ {
		entries[order[k]].rounded++
	}

	// Build a deterministic closure permutation over the SURVIVOR
	// positions: the first N survivors close in 2011, next in 2012, etc.
	perm := make([]int, len(survivors))
	for i := range perm {
		perm[i] = i
	}
	permRNG := rng.Derive(seed, "shops/closure_perm")
	permRNG.Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })

	dateRNG := rng.Derive(seed, "shops/closure_date")
	cursor := 0
	for _, e := range entries {
		yearStart := time.Date(e.year, time.January, 1, 0, 0, 0, 0, time.UTC)
		yearEnd := time.Date(e.year, time.December, 31, 0, 0, 0, 0, time.UTC)
		if e.year == 2016 {
			// Front-load: nothing closes after the official liquidation
			// date. Distribution within the year stays uniform — Chapter
			// 11 filings, fire-sale wind-downs, and final liquidation
			// run continuously from Feb through Sep.
			yearEnd = policy.BusinessClosureDate
		}
		span := yearEnd.Sub(yearStart)
		reason := policy.ClosureReasonForYear(e.year)
		for i := 0; i < e.rounded; i++ {
			if cursor >= len(perm) {
				break
			}
			shopIdx := survivors[perm[cursor]]

			// Don't close a shop before it opened. Edge case: a shop
			// opened in 2010 might get assigned a 2011 closure year and
			// land at e.g. 2011-01-15 even though its opened_date is
			// 2010-12-30 — that's still valid. But if opened_date is
			// after yearStart, clamp closure date to opened+30d.
			opened, err := time.Parse("2006-01-02", shops[shopIdx].OpenedDate)
			if err == nil && opened.After(yearStart) {
				// Shift window so closure can't predate opening.
				minClose := opened.Add(30 * 24 * time.Hour)
				if minClose.After(yearEnd) {
					// Shop opened too close to year-end; push closure
					// into the next available year (skip-ahead is rare
					// and only affects shops at the edge of the curve).
					minClose = yearEnd
				}
				adjSpan := yearEnd.Sub(minClose)
				if adjSpan < 0 {
					adjSpan = 0
				}
				offset := time.Duration(dateRNG.Float64() * float64(adjSpan))
				closeDate := minClose.Add(offset).Format("2006-01-02")
				shops[shopIdx].ClosedDate = &closeDate
			} else {
				offset := time.Duration(dateRNG.Float64() * float64(span))
				closeDate := yearStart.Add(offset).Format("2006-01-02")
				shops[shopIdx].ClosedDate = &closeDate
			}
			shops[shopIdx].ClosureReason = reason
			cursor++
		}
	}
}

// shopNameFor constructs a shop name incorporating the city. City
// names can repeat across the estate (multiple NYC branches); the
// shop_code provides the uniqueness.
//
// Output is bounded to shopNameMaxRunes characters by truncating the
// city portion if needed. GeoNames Canadian FSA entries have verbose
// district descriptors that can exceed 100 chars; without truncation
// the V1.8.1 retail.shops.name column (NVARCHAR(120)) overflows on
// tiers with 7000+ shops where the long entries get sampled. V1.8.2
// schema raised the column to NVARCHAR(255) but the generator still
// caps so we don't depend on the schema being current.
const shopNameMaxRunes = 240

func shopNameFor(country, city string) string {
	if city == "" {
		return fmt.Sprintf("Nick's Gaming Emporium %s", country)
	}
	const prefix = "Nick's Gaming Emporium "
	available := shopNameMaxRunes - len([]rune(prefix))
	if rs := []rune(city); len(rs) > available {
		city = string(rs[:available])
	}
	return prefix + city
}

// usFirstCount is min(USFirstShopCount, total).
func usFirstCount(total int) int {
	if total < policy.USFirstShopCount {
		return total
	}
	return policy.USFirstShopCount
}

// buildDatePools produces per-country opening-date slices honouring
// market-entry dates:
//
//   - US: US-0001 anchored to the exact founding day (policy.FoundingDate,
//     1986-08-06) so shop_id 1 is always Chicago-on-founding-day; the next
//     min(30, total_US)-1 dates sample the US-first era (founding→1998),
//     remaining from continuation (1996–2010 S-curve).
//   - GB: first shop anchored exactly to 1996-06-01 (10-year
//     anniversary per §9.15.4), remaining sampled from 1996-06-01
//     through 2015-12-31 (UK-expansion era overlaps US-continuation).
//   - All others: sampled from InternationalMarketEntry (1999-01-01)
//     through 2015-12-31, clamped to asOf.
//
// Each country's dates are pre-sorted ascending so the caller can
// iterate by index and get the country's shops opened-in-order.
func buildDatePools(seed uint64, total int, asOf time.Time, allocations map[string]int) map[string][]time.Time {
	pools := make(map[string][]time.Time, len(allocations))
	intlEnd := clampDate(policy.InternationalExpansionEnd, asOf)

	for _, share := range policy.ShopShares {
		n := allocations[share.Country]
		if n == 0 {
			continue
		}

		var dates []time.Time
		switch share.Country {
		case "US":
			usFirstN := usFirstCount(total)
			// V1.23.0: US-0001 anchored to the exact founding day so shop_id 1
			// is always Chicago-on-1986-08-06 (matches the founding milestone
			// + COMPANY_HISTORY.md) at every tier and seed.
			// V1.24.1: US-0002 anchored to the flagship day (1986-09-28 — the
			// New York store, the lore's thirty-year symmetry) after the
			// V1.23.0 pin's resampling silently moved it. The remaining
			// us_first shops sample from the day AFTER the flagship, so the
			// two anchors are guaranteed to be the two earliest openings and
			// keep their shop codes. Mirrors the GB-0001 anniversary anchor
			// below.
			usFirstDates := []time.Time{policy.FoundingDate}
			if usFirstN >= 2 {
				usFirstDates = append(usFirstDates, policy.FlagshipDate)
			}
			if rem := usFirstN - 2; rem > 0 {
				usFirstDates = append(usFirstDates, sampleDates(
					seed, "shops/opening/us_first",
					policy.FlagshipDate.AddDate(0, 0, 1),
					clampDate(policy.USFirstEraEnd, asOf),
					rem,
				)...)
			}
			var usRemaining []time.Time
			if rem := n - usFirstN; rem > 0 {
				// V1.19: S-curve (was uniform) over the 1996-2010 window
				// so US continuation bridges the US-first era smoothly.
				usRemaining = sampleDatesSCurve(
					seed, "shops/opening/us_continuation",
					policy.InternationalExpansionStart, intlEnd, rem,
				)
			}
			dates = append(usFirstDates, usRemaining...)
		case "GB":
			// UK-0001 anchored to the 10-year anniversary. Remaining
			// UK shops sampled in UK's full market-entry-to-2015 range.
			anchor := policy.MarketEntry("GB")
			if n > 1 {
				dates = sampleDatesSCurve(
					seed, "shops/opening/gb_remaining",
					anchor, intlEnd, n-1,
				)
			}
			dates = append(dates, anchor)
		default:
			// V1.19: international branches open on an S-curve from their
			// 1999 market entry — the curve's near-zero density at the
			// window edge makes 1999 a gentle ramp-in, not the old cliff.
			entry := policy.MarketEntry(share.Country)
			dates = sampleDatesSCurve(
				seed, "shops/opening/"+share.Country,
				entry, intlEnd, n,
			)
		}

		sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
		pools[share.Country] = dates
	}
	return pools
}

// allocatePerCountry returns per-country shop counts summing to
// `total`, enforcing the US-first minimum and using largest-remainder
// rounding across non-US countries.
func allocatePerCountry(total int) map[string]int {
	alloc := make(map[string]int, len(policy.ShopShares))

	usMin := usFirstCount(total)
	var usShare float64
	for _, s := range policy.ShopShares {
		if s.Country == "US" {
			usShare = s.Share
			break
		}
	}
	usCount := int(math.Round(usShare * float64(total)))
	if usCount < usMin {
		usCount = usMin
	}
	if usCount > total {
		usCount = total
	}
	alloc["US"] = usCount

	remaining := total - usCount
	var nonUSShareSum float64
	for _, s := range policy.ShopShares {
		if s.Country != "US" {
			nonUSShareSum += s.Share
		}
	}
	if remaining == 0 || nonUSShareSum == 0 {
		for _, s := range policy.ShopShares {
			if s.Country != "US" {
				alloc[s.Country] = 0
			}
		}
		return alloc
	}

	type entry struct {
		country   string
		ideal     float64
		allocated int
	}
	entries := make([]entry, 0, len(policy.ShopShares)-1)
	usedUp := 0
	for _, s := range policy.ShopShares {
		if s.Country == "US" {
			continue
		}
		ideal := float64(remaining) * (s.Share / nonUSShareSum)
		a := int(ideal)
		entries = append(entries, entry{s.Country, ideal, a})
		usedUp += a
	}
	leftover := remaining - usedUp
	sort.SliceStable(entries, func(i, j int) bool {
		fi := entries[i].ideal - float64(entries[i].allocated)
		fj := entries[j].ideal - float64(entries[j].allocated)
		return fi > fj
	})
	for k := 0; k < leftover && k < len(entries); k++ {
		entries[k].allocated++
	}
	for _, e := range entries {
		alloc[e.country] = e.allocated
	}
	return alloc
}

// sampleDates returns n deterministic dates uniformly sampled in
// [start, end], sorted ascending.
func sampleDates(seed uint64, namespace string, start, end time.Time, n int) []time.Time {
	if n == 0 {
		return nil
	}
	r := rng.Derive(seed, namespace)
	span := end.Sub(start)
	if span <= 0 {
		return nil
	}
	dates := make([]time.Time, n)
	for i := 0; i < n; i++ {
		offset := time.Duration(r.Int64N(int64(span)))
		dates[i] = start.Add(offset)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	return dates
}

// sampleDatesSCurve returns n deterministic dates over [start, end] with
// a smooth S-curve cumulative distribution (bell-shaped density centred
// in the window, tapering to near-zero at both ends), sorted ascending.
//
// V1.19: replaces a uniform fill for the 1996-2010 expansion era to kill
// the "1999 cliff" — pre-V1.19, a uniform window gated at 1999 dumped a
// wall of openings into that one year (159 shops in 1998 → 705 in 1999).
// Uses the Bates distribution (mean of three uniforms): bounded, no
// boundary pile-ups, peak near the window centre (~2003 for 1996-2010),
// so the estate ramps up gently, plateaus mid-decade, and tapers toward
// the 2010 freeze. Cumulative open-count still rises monotonically to
// the 2010 peak (no openings after, no closures before 2011).
func sampleDatesSCurve(seed uint64, namespace string, start, end time.Time, n int) []time.Time {
	if n == 0 {
		return nil
	}
	r := rng.Derive(seed, namespace)
	span := end.Sub(start)
	if span <= 0 {
		return nil
	}
	dates := make([]time.Time, n)
	for i := 0; i < n; i++ {
		frac := (r.Float64() + r.Float64() + r.Float64()) / 3.0
		offset := time.Duration(frac * float64(span))
		dates[i] = start.Add(offset)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	return dates
}

// generateTransientShops builds the V1.19 early-churn cohort: shops that
// opened AND closed within the expansion window (lease_expiry churn).
// They use fresh RNG namespaces ("shops/churn/...") so survivor
// geography/dates are unperturbed, and ALL close by 2010-12-30 so the
// 2010-12-31 peak estate (== ShopCountByTier) is preserved. Openings are
// biased early (1996-2005) with short lives (1-4y) so closures cluster
// pre-2010 and few overlap the 2007-12-31 profit-peak snapshot — keeping
// "the estate grew to its peak by end-2010" true. Returns nil for
// as-of snapshots that predate the churn window's close.
func generateTransientShops(seed uint64, asOf time.Time, postals *geography.Index, churnCount int) ([]Shop, error) {
	if churnCount <= 0 {
		return nil, nil
	}
	winClose := time.Date(2010, time.December, 30, 0, 0, 0, 0, time.UTC)
	if asOf.Before(winClose) {
		return nil, nil
	}
	openStart := policy.InternationalExpansionStart                    // 1996-01-01
	openEnd := time.Date(2005, time.December, 31, 0, 0, 0, 0, time.UTC) // open early so lives end pre-2010
	openSpan := openEnd.Sub(openStart)
	if openSpan <= 0 {
		return nil, nil
	}

	alloc := allocateChurnPerCountry(churnCount)
	openRNG := rng.Derive(seed, "shops/churn/open")
	lifeRNG := rng.Derive(seed, "shops/churn/life")

	out := make([]Shop, 0, churnCount)
	for _, share := range policy.ShopShares {
		n := alloc[share.Country]
		if n == 0 {
			continue
		}
		if postals.CountFor(share.Country) == 0 {
			continue // optional cohort — skip countries with no geography
		}
		postalRNG := rng.Derive(seed, "shops/churn/postal/"+share.Country)
		streetRNG := rng.Derive(seed, "shops/churn/street/"+share.Country)
		for i := 0; i < n; i++ {
			// Bates(2) over 1996-2005 → triangular, peaks ~2000-2001.
			frac := (openRNG.Float64() + openRNG.Float64()) / 2.0
			opened := openStart.Add(time.Duration(frac * float64(openSpan)))
			lifeYears := 1.0 + lifeRNG.Float64()*3.0
			closed := opened.Add(time.Duration(lifeYears * 365.25 * 24 * float64(time.Hour)))
			if closed.After(winClose) {
				closed = winClose
			}
			if !closed.After(opened) {
				continue
			}
			pc, _ := postals.Sample(share.Country, postalRNG)
			addr := ShopAddress{
				Line1:       geography.GenerateStreetAddress(share.Country, pc.City, streetRNG),
				City:        pc.City,
				Region:      pc.Region,
				PostalCode:  pc.Postal,
				CountryCode: share.Country,
				Latitude:    pc.Latitude,
				Longitude:   pc.Longitude,
			}
			openedStr := opened.Format("2006-01-02")
			closedStr := closed.Format("2006-01-02")
			out = append(out, Shop{
				ShopCode:      fmt.Sprintf("%s-T%04d", share.Country, i+1),
				Name:          shopNameFor(share.Country, pc.City),
				CountryCode:   share.Country,
				CurrencyCode:  share.CurrencyCode,
				OpenedDate:    openedStr,
				ClosedDate:    &closedStr,
				ClosureReason: "lease_expiry",
				SourceSystem:  policy.SourceSystemForYear(opened.Year()),
				Address:       addr,
			})
		}
	}
	return out, nil
}

// allocateChurnPerCountry spreads the transient churn cohort across
// countries by ShopShares (largest-remainder), with no US-first floor —
// churn is global. Returns per-country counts summing to total.
func allocateChurnPerCountry(total int) map[string]int {
	alloc := make(map[string]int, len(policy.ShopShares))
	if total <= 0 {
		return alloc
	}
	var shareSum float64
	for _, s := range policy.ShopShares {
		shareSum += s.Share
	}
	if shareSum == 0 {
		return alloc
	}
	type entry struct {
		country   string
		ideal     float64
		allocated int
	}
	entries := make([]entry, 0, len(policy.ShopShares))
	used := 0
	for _, s := range policy.ShopShares {
		ideal := float64(total) * (s.Share / shareSum)
		a := int(ideal)
		entries = append(entries, entry{s.Country, ideal, a})
		used += a
	}
	leftover := total - used
	sort.SliceStable(entries, func(i, j int) bool {
		fi := entries[i].ideal - float64(entries[i].allocated)
		fj := entries[j].ideal - float64(entries[j].allocated)
		return fi > fj
	})
	for k := 0; k < leftover && k < len(entries); k++ {
		entries[k].allocated++
	}
	for _, e := range entries {
		alloc[e.country] = e.allocated
	}
	return alloc
}

func clampDate(d, limit time.Time) time.Time {
	if d.After(limit) {
		return limit
	}
	return d
}
