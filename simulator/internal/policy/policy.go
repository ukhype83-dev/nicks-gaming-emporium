// Package policy embeds the §9 design-spec tables as Go data so the
// simulator runs against them without reading YAML / config files.
//
// When policy values change (they will), the `simulator_version` in
// meta.build_metadata must be bumped — see §9.10.7 reproducibility
// contract. Policy changes are breaking changes.
package policy

import (
	"math"
	"math/rand/v2"
	"time"
)

// --------------------------------------------------------------------
// §9.9 / §9.15.4 — tier sizing and shop counts
// --------------------------------------------------------------------

// ShopCountByTier is the target total shop count at the as-of date,
// per §9.15.4. Calibration history:
//   V1.7.0 — declared sizes were 39-80% of actual GB. Tiers scaled
//            sub-linearly with naming, customers over-provisioned.
//   V1.8.0 — recalibrated based on V1.7 measurements (~1.8 shops/GB).
//            30g landed at 102% (perfect). 300g landed at 79% — V1.8
//            measurements showed bytes-per-shop drop slightly at scale
//            (470 MB/shop at 30g, 431 MB/shop at 300g) likely from
//            page-fill efficiency on larger heaps.
//   V1.8.1 — bumped 300g 550→700 and 3t 5500→7000 to compensate for
//            the per-shop shrinkage observed at scale.
var ShopCountByTier = map[string]int{
	"3g":   10,   // not retargeted — no production-size measurement on this tier
	"30g":  65,   // V1.8.0 calibration confirmed at 30.6 GB / 30 GB (102%)
	"300g": 700,  // V1.8.0 hit 237 GB / 300 GB (79%); 700 = 550 × (300/237)
	"3t":   7000, // V1.8.1 extrapolation; untested at this scale
}

// AnchorCity names a postal-codes city + optional admin1 code for
// shop-placement anchoring. Region disambiguates same-named places
// across states/regions: ("Atlanta", "GA") vs Atlanta ID. Leave Region
// empty when the city name is unambiguous in the country (e.g. Phoenix,
// Las Vegas, San Diego).
type AnchorCity struct {
	City   string
	Region string // admin_code1; "" allows any region
}

// CountryShopShare captures one row of §9.15.4's shop-distribution
// table. Shares sum to 1.0.
//
// AnchorCities (V1.13.6): a list of metros that should reliably get at
// least one shop before the rest of the country's shop allocation is
// filled by population-weighted random sampling. Without anchors,
// random sampling at 30g (~30 US shops) routinely leaves major metros
// (LA, Phoenix, Seattle) shopless purely by luck of the seed, which
// makes those cities' customer counts look implausibly low. Anchors
// are honoured in order; if more anchors are listed than slots are
// available (e.g. 3g/10 shops with 20 anchors), the trailing anchors
// are dropped. Anchors that don't match any postal-codes city fall
// back to random.
type CountryShopShare struct {
	Country      string       // ISO 3166-1 alpha-2
	Name         string       // human-readable
	Share        float64      // fraction of NGE's global shop estate
	CurrencyCode string       // ISO 4217; the shop's reporting currency
	AnchorCities []AnchorCity // V1.13.6 — see struct doc above
}

// ShopShares — per §9.15.4. Order is load-bearing: deterministic
// generation iterates this slice in order. Editing the order or adding
// rows is a breaking change that requires bumping simulator_version.
// V1.19: re-weighted US-heavy (~55%) to match a real specialty-games
// chain's footprint (GameStop peaked ~58% US). Japan and Korea are
// thinned to a token presence (1.5% / 0.5%) — no Western games retailer
// ever ran a large JP/KR estate. The other 15 countries are the old
// non-{US,JP,KR} shares renormalised to fill the remaining 0.430 (the
// old block summed to 0.61). Order is still load-bearing; GB carries the
// rounding residual so the slice sums to exactly 1.000. CustomerShares
// (a separate table) is intentionally NOT changed.
var ShopShares = []CountryShopShare{
	{"US", "United States", 0.550, "USD", []AnchorCity{
		// V1.13.6/7: top US metros that random sampling has historically
		// missed at 30g. Region (admin_code1) specified for the cities
		// that exist as small towns in many other states (Atlanta,
		// Boston, Portland, Charlotte, San Antonio, Las Vegas, Miami);
		// truly unambiguous mega-metros (Chicago, Houston, Phoenix)
		// leave Region empty.
		// V1.17.0: Chicago moved to slot 0 — anchor slot order maps to
		// shop-code order, and US-0001 (opened on founding day
		// 1986-08-06) must be Chicago to match the founding milestone
		// and COMPANY_HISTORY.md. Pre-V1.17 builds had shop_id 1 = New
		// York, contradicting the narrative (training-catalog audit
		// finding F5a).
		{"Chicago", "IL"},
		{"New York", "NY"}, // Manhattan ZIPs 10001-10282 (V1.13.5 alias)
		{"Los Angeles", "CA"},
		{"Houston", "TX"},
		{"Phoenix", "AZ"},
		{"Philadelphia", "PA"},
		{"San Diego", "CA"},
		{"Dallas", "TX"},
		{"San Antonio", "TX"}, // San Antonio FL is small
		{"Atlanta", "GA"},     // V1.13.6 landed on Atlanta ID
		{"Seattle", "WA"},
		{"Las Vegas", "NV"},  // Las Vegas NM exists
		{"Miami", "FL"},      // Miami OK/AZ exist
		{"Boston", "MA"},     // V1.13.6 landed on Boston IN
		{"Detroit", "MI"},    // Detroit TX exists
		{"Minneapolis", "MN"},
		{"Denver", "CO"},     // Denver CO ambiguous w/ Denver NC/PA
		{"Portland", "OR"},   // Portland ME also major
		{"Indianapolis", "IN"},
		{"Charlotte", "NC"},  // Charlotte MI/VT exist
	}},
	{"GB", "United Kingdom", 0.073, "GBP", nil}, // carries the rounding residual
	{"DE", "Germany", 0.049, "EUR", nil},
	{"BR", "Brazil", 0.042, "BRL", nil},
	{"FR", "France", 0.042, "EUR", nil},
	{"CA", "Canada", 0.035, "CAD", nil},
	{"JP", "Japan", 0.015, "JPY", nil}, // V1.19 thinned (was 0.05)
	{"AU", "Australia", 0.028, "AUD", nil},
	{"IT", "Italy", 0.028, "EUR", nil},
	{"ES", "Spain", 0.028, "EUR", nil},
	{"NL", "Netherlands", 0.021, "EUR", nil},
	{"CH", "Switzerland", 0.014, "CHF", nil},
	{"KR", "South Korea", 0.005, "KRW", nil}, // V1.19 thinned (was 0.02)
	{"PL", "Poland", 0.014, "PLN", nil},
	{"SE", "Sweden", 0.014, "SEK", nil},
	{"CZ", "Czech Republic", 0.014, "CZK", nil},
	{"DK", "Denmark", 0.014, "DKK", nil},
	{"NO", "Norway", 0.014, "NOK", nil},
}

// --------------------------------------------------------------------
// §9.15.4 — shop-opening phases
//
// NGE's shop network grows in three distinct phases:
//
//   1. US-first establishment (1986-06 through 1998-12). USFirstShopCount
//      US-only shops open during this era. No international presence —
//      Nick proves the model domestically before going global.
//   2. International expansion (1999-01 through 2015-12). Remaining
//      shops open — additional US growth plus all non-US branches.
//   3. Consolidation (2016+). Net flat: closures offset by replacement
//      openings. Not modelled in the skeleton.
// --------------------------------------------------------------------

// USFirstShopCount is the number of US shops established before NGE
// goes international. A tier with fewer than this many shops total is
// effectively a snapshot of NGE during the US-only era, still 100% US.
const USFirstShopCount = 30

// NGE's founding month (June 1986) and the date international
// expansion began.
//
// V1.15.0 narrative: NGE files Chapter 11 on 2016-02-08 after years
// of declining same-store sales (digital distribution + the financial
// crisis killing mall foot traffic). New-shop openings stop at the
// 2010 peak; the estate winds down 2011-2016. See BusinessClosureDate.
var (
	// FoundingDate is the exact day US-0001 (Chicago) opened — the first
	// shop, shop_id 1. V1.23.0: anchored so shop_id 1 is deterministically
	// Chicago-on-1986-08-06 at EVERY tier and seed (shops.buildDatePools
	// places it as the guaranteed global-minimum opened_date). Pre-V1.23
	// the founding day was the min of tier-dependent us_first samples, so
	// it only matched the hard-coded founding milestone at the 30-sample
	// tiers (30g/300g/3T) and drifted at 3g. This is the single source of
	// truth for the date; dbo.business_milestones' "founding" row and
	// COMPANY_HISTORY.md both cite it.
	FoundingDate = time.Date(1986, time.August, 6, 0, 0, 0, 0, time.UTC)

	// FlagshipDate is the day US-0002 (New York — Jean and Nick's own
	// store) opened. V1.24.1: pinned after the V1.23.0 founding pin's
	// date-resampling silently moved it to 1986-10-10 in the first
	// build that carried the pin — breaking the lore's load-bearing
	// symmetry (flagship opened 1986-09-28, closed 2016-09-27: one day
	// short of thirty years). Both founding dates are canon; both are
	// now pinned. The remaining US-first shops sample from the day
	// AFTER the flagship so ordering (and therefore shop_code / shop_id
	// assignment) is guaranteed.
	FlagshipDate = time.Date(1986, time.September, 28, 0, 0, 0, 0, time.UTC)

	USFirstEraStart = time.Date(1986, time.June, 1, 0, 0, 0, 0, time.UTC)
	USFirstEraEnd   = time.Date(1998, time.December, 31, 0, 0, 0, 0, time.UTC)
	// V1.19: the expansion curve now starts in 1996 (was 1999) so US
	// "continuation" openings bridge the US-first era smoothly instead of
	// stepping off a cliff at 1999. This is the START of the S-curve
	// opening window (see shops.sampleDatesSCurve); it overlaps the
	// US-first era by design. Distinct from InternationalMarketEntry
	// (1999), which still gates NON-US shop entry and non-US customer
	// signups — international branches join from 1999, ramping in gently
	// via the S-curve's near-zero density at the window edge.
	InternationalExpansionStart = time.Date(1996, time.January, 1, 0, 0, 0, 0, time.UTC)
	// V1.15.0: last new-shop opening date (was 2015-12-31 pre-V1.15.0).
	// The 2007 peak was followed by a slow opening tail through 2010,
	// then nothing — same-store decline became too severe to justify
	// new sites.
	InternationalExpansionEnd = time.Date(2010, time.December, 31, 0, 0, 0, 0, time.UTC)

	// V1.15.0 — closure-era anchors.
	BusinessPeakDate    = time.Date(2007, time.December, 31, 0, 0, 0, 0, time.UTC)
	BusinessClosureDate = time.Date(2016, time.September, 30, 0, 0, 0, 0, time.UTC)
)

// V1.15.0 — shop closure schedule.
//
// Per-year closure fractions across the 2011-2016 wind-down. Sums to
// 1.0 — every shop opens before 2011 and closes by 2016-09-30.
// Front-loaded to 2016 to match a real Chapter 11 liquidation curve.
//
// At tier=300g (700 shops) this maps to 5/10/30/105/200/350 closures
// per year; scaled proportionally to other tiers via Generate.
var ClosureCurveFractions = []struct {
	Year     int
	Fraction float64
}{
	{2011, 5.0 / 700.0},
	{2012, 10.0 / 700.0},
	{2013, 30.0 / 700.0},
	{2014, 105.0 / 700.0},
	{2015, 200.0 / 700.0},
	{2016, 350.0 / 700.0},
}

// ClosureReasonForYear maps a closure year to the canonical
// closure_reason string stored in retail.shops.closure_reason.
func ClosureReasonForYear(y int) string {
	switch {
	case y <= 2012:
		return "lease_expiry"
	case y == 2013:
		return "underperformance"
	case y == 2014:
		return "restructuring_2014"
	case y == 2015:
		return "restructuring_2015"
	default:
		return "liquidation_2016"
	}
}

// V1.19 — early-era store churn.
//
// Before the 2011 wind-down, NGE still closed and replaced
// underperforming sites at a low annual rate (real chains always churn
// locations — a zero-closure 1986-2010 estate is a synthetic tell). The
// generator models this as a transient cohort of shops that open AND
// close within the 1996-2010 expansion window (lease_expiry churn). The
// transient cohort is generated OUTSIDE the peak estate so the
// 2010-12-31 count (== ShopCountByTier, the EstatePeak narrative
// divisor) is unchanged. churnCount ≈ EarlyChurnAnnualRate × peak × 12
// expansion-years. See shops.generateTransientShops.
const EarlyChurnAnnualRate = 0.015

// V1.15.0 — transaction volume taper.
//
// Year-by-year revenue multiplier vs the 2010 peak. After 2010 the
// network shrinks and same-store sales collapse; by 2016 only fire-
// sale clearance volume remains, and operations stop entirely on
// 2016-09-30. Used inside the daily transaction loop to scale the
// expected-count Poisson rate.
var TransactionVolumeMultiplier = map[int]float64{
	2010: 1.00, // peak
	2011: 0.96,
	2012: 0.88,
	2013: 0.78,
	2014: 0.62,
	2015: 0.42,
	2016: 0.30, // Chapter 11 fire sale; cuts to zero after 2016-09-30
}

// TransactionVolumeFor returns the year-multiplier to apply on top of
// the policy baseline transaction rate. Pre-2010 years are at 1.0
// (the existing baseline is calibrated to peak); post-2016 returns 0.
func TransactionVolumeFor(year int) float64 {
	if year < 2010 {
		return 1.0
	}
	if v, ok := TransactionVolumeMultiplier[year]; ok {
		return v
	}
	return 0.0
}

// V1.15.0 — channel-mix during NGE's decline is handled by
// ChannelMixForYear below; in-store share collapses from ~50% (2014)
// to ~28% (2016) as the cause of the bankruptcy.

// V1.15.0 — wage curve.
//
// Annual cost-of-living adjustment applied to all current employees.
const WageCOLAPerYear = 0.035

// Tenure premium for staff hired before 2000 (legacy contracts).
const WageTenurePremiumPerYear = 0.015
const WageTenurePremiumCutoffYear = 2000

// Fraction of workforce eligible for retention bonus during decline
// (managers + senior individual contributors).
const RetentionBonusEligibleFraction = 0.15
const RetentionBonusUplift = 0.20 // +20% on base
var RetentionBonusEffectiveFrom = time.Date(2014, time.September, 1, 0, 0, 0, 0, time.UTC)

// Wind-down crew premium (Q2-Q3 2016).
const WindDownCrewPremium = 0.50 // +50% on base
const WindDownCrewSize = 200     // for tier=300g (scaled)

// Severance: weeks of base pay per year of service, capped at 52
// weeks (1 year of pay) per industry norm.
const SeveranceWeeksPerYearOfService = 8
const SeveranceWeeksMax = 52

// V1.19 — annual performance bonus.
//
// All staff above entry level earn a grade-scaled annual bonus in
// profitable years. The bonus is a REALISM feature, not part of the
// death-spiral mechanism: real firms pay generously when profitable and
// cut bonuses first when losses hit. It is paid each March (for the
// prior fiscal year) and never compounds into base pay. See
// hr.generateCompensation, which emits single-day 'bonus' rows.

// BonusFractionForGrade returns the target bonus as a fraction of base
// annual wage for a pay grade. Entry-level (G1) hourly staff get none;
// the fraction climbs steeply through management to executives, whose
// bonus can match base pay (total comp ≈ 2× base in a good year).
func BonusFractionForGrade(grade int) float64 {
	switch grade {
	case 1:
		return 0.00
	case 2:
		return 0.03
	case 3:
		return 0.05
	case 4:
		return 0.08
	case 5:
		return 0.12
	case 6:
		return 0.18
	case 7:
		return 0.30
	case 8:
		return 0.45
	case 9:
		return 0.65
	case 10:
		return 1.00
	default:
		return 0.0
	}
}

// BonusPayoutFactorForYear gates bonuses by company profitability. The
// program starts in 1996 (post-establishment growth) and runs at full
// payout through the 2010 peak; as losses mount in the decline the board
// cuts bonuses — halved in 2011, quartered in 2012, and zero from 2013
// as the company bleeds toward Chapter 11.
func BonusPayoutFactorForYear(year int) float64 {
	switch {
	case year < 1996:
		return 0.0
	case year <= 2010:
		return 1.0
	case year == 2011:
		return 0.5
	case year == 2012:
		return 0.25
	default: // 2013-2016: losses, bonuses cut entirely
		return 0.0
	}
}

// V1.16.5 — corporate-wage maturity ramp.
//
// Early-stage NGE couldn't pay corporate staff (HQ, senior leadership,
// and the founders Nick Reeves & Jean Macy) full market rates. A
// bootstrapped retailer funded by a small family loan conserves cash
// and runs on owner-operator / equity-culture pay until it reaches
// the scale to afford a real corporate function. This factor scales
// corporate base wages from CorporateMaturityFloor in the 1986
// founding year up to 1.0 by CorporateMaturityFullYear (2001, when
// NGE crossed into sustained profitability). Retail shop staff are
// exempt — they always earned market hourly pay.
const corporateMaturityFloor = 0.20
const corporateMaturityStartYear = 1986
const corporateMaturityFullYear = 2001

// CorporateMaturityFactor returns the early-era wage multiplier for
// corporate (non-retail) staff in the given year. Linear ramp from
// the floor (1986) to 1.0 (2001+).
func CorporateMaturityFactor(year int) float64 {
	if year <= corporateMaturityStartYear {
		return corporateMaturityFloor
	}
	if year >= corporateMaturityFullYear {
		return 1.0
	}
	frac := float64(year-corporateMaturityStartYear) /
		float64(corporateMaturityFullYear-corporateMaturityStartYear)
	return corporateMaturityFloor + (1.0-corporateMaturityFloor)*frac
}

// V1.19 — founder draw ramp.
//
// The two founders (Nick Reeves & Jean Macy) are corporate staff but
// must ALWAYS out-earn any shop manager, even in the lean early years —
// the pre-V1.19 data inverted this (a 1986 CEO earned less than a store
// manager because CorporateMaturityFactor damped the founders down to
// the 0.20 floor). They get their own gentler ramp with a higher floor:
// an owner-operator still draws a real salary out of a bootstrapped
// company, just not the full modern executive package. Combined with
// the raised executive grade anchor (G10 = $1.05M, see
// hr.annualWageBaseline) this keeps CEO > Store Manager in every year
// 1986-2016. Same ramp endpoints as CorporateMaturityFactor (1986 →
// 2001).
const founderDrawFloor = 0.45

func FounderDrawFactor(year int) float64 {
	if year <= corporateMaturityStartYear {
		return founderDrawFloor
	}
	if year >= corporateMaturityFullYear {
		return 1.0
	}
	frac := float64(year-corporateMaturityStartYear) /
		float64(corporateMaturityFullYear-corporateMaturityStartYear)
	return founderDrawFloor + (1.0-founderDrawFloor)*frac
}

// IsFounderRole reports whether a role is one of the two founders (CEO
// role 19, Co-Founder role 20). Founder roles use FounderDrawFactor
// instead of the broader CorporateMaturityFactor. See Roles.
func IsFounderRole(roleID int) bool {
	return roleID == 19 || roleID == 20
}

// --------------------------------------------------------------------
// §9.9 / §9.12.7 — customer totals and per-country allocation
// --------------------------------------------------------------------

// CustomerCountByTier is the target total customer count at the as-of
// date, per §9.9.
//
// V1.25.0 — the membership realism rebalance. The V1.8 extrapolation
// landed the 3T tier at 380M members: MORE than the US population and
// ~7× GameStop's real PowerUp Rewards peak (~40-55M) — while the
// identified-transaction density worked out to ~1.4 purchases per
// member per LIFETIME, which is a mailing list, not a loyalty
// programme. Retargeted to a GameStop-scale 40M cumulative members at
// 3T (~13.5 identified purchases per member — genuine loyalty-member
// territory), scaling 10× per tier. Customer-side tables shrink ~9.4×
// (~400-600GB at 3T); the transaction side — and therefore revenue
// calibration and the death spiral — is untouched. The reclaimed size
// budget is earmarked for the review system (V1.26 candidate).
var CustomerCountByTier = map[string]int{
	"3g":   40_000,
	"30g":  400_000,
	"300g": 4_000_000,
	"3t":   40_000_000,
}

// CountryCustomerShare — per §9.12.7. Approximate population-weighted
// shares across the 18 countries NGE operates in. Order is load-
// bearing; editing is a breaking change.
type CountryCustomerShare struct {
	Country string
	Share   float64
	Regime  string // governing privacy regime per §9.12.4
}

// CustomerShares sum to 1.0. US absorbs the "rest of world" gap — the
// 18 countries in §9.12.7's table were population-weighted across NGE's
// operating markets only (~75% of world pop), and we dump the remaining
// 25% onto US rather than invent a rest-of-world bucket. This also lines
// up with §9.15.3's statement that NA accounts for 40–45% of NGE's
// global transaction volume.
var CustomerShares = []CountryCustomerShare{
	{"US", 0.448, "ccpa"},
	{"BR", 0.13, "lgpd"},
	{"JP", 0.08, "appi"},
	{"DE", 0.05, "gdpr"},
	{"GB", 0.04, "uk_gdpr"},
	{"FR", 0.04, "gdpr"},
	{"IT", 0.04, "gdpr"},
	{"KR", 0.03, "none"},
	{"ES", 0.03, "gdpr"},
	{"CA", 0.025, "pipeda"},
	{"PL", 0.025, "gdpr"},
	{"AU", 0.02, "none"},
	{"NL", 0.013, "gdpr"},
	{"SE", 0.007, "gdpr"},
	{"CZ", 0.007, "gdpr"},
	{"CH", 0.007, "none"},
	{"DK", 0.004, "gdpr"},
	{"NO", 0.004, "none"},
}

// --------------------------------------------------------------------
// §9.10 — customer signup-era distribution
// --------------------------------------------------------------------

// SignupEra partitions customer signups across NGE's 40-year life,
// reflecting the §9.10.6 customer-identity timeline.
type SignupEra struct {
	StartYear int
	EndYear   int
	Share     float64
}

// SignupEras shares sum to 1.0. Non-US countries clamp start to 1999
// (international expansion begin) — see customers package.
//
// V1.14.0: pre-online shares dropped ~10× (1986-1994: 0.01 → 0.001,
// 1995-2003: 0.08 → 0.04). The previous distribution implied ~20k
// new US loyalty signups per year in the 1980s for a chain with only
// ~10-15 US shops — implausible. Real 1980s retail loyalty programs
// were tiny (store credit holders + warranty registrants + cheque
// account customers); ~150 signups/shop/year is the realistic figure.
// The "missing" 1980s customers aren't lost — they just shop
// anonymously, which is reflected in CustomerLinkageProbability's
// 5% in-store linkage rate pre-1995.
//
// V1.17.0: the customer clock now ends at liquidation. V1.14.0 had
// parked 50% of signup weight in 2017-2025 buckets — but V1.15.0
// killed the company on 2016-09-30, leaving 51% of customers at 3T
// "signing up" at a liquidated retailer (training-catalog audit
// finding F1; the transactions and HR clocks were capped in V1.15.0,
// this one was missed). The 2017+ weight folds into the online eras:
// signups stay heavily back-loaded, which matches both real loyalty-
// programme growth and the unified_2011 account push. Customer COUNT
// per tier is unchanged (CustomerCountByTier) — dates compress, rows
// don't disappear — so tier sizing and the customer-table row counts
// are not recalibrated by this change.
var SignupEras = []SignupEra{
	{1986, 1994, 0.001}, // V1.14.0: paper loyalty stamps; ~150/shop/yr
	{1995, 2003, 0.04},  // V1.14.0: barcoded cards + early email capture
	{2004, 2010, 0.339}, // V1.17.0: online era begins (absorbs 2017-2020 weight)
	{2011, 2016, 0.62},  // V1.17.0: unified accounts + final-era capture (absorbs 2021-2025)
}

// SignupCutoff is the last date a customer can sign up — the company
// stopped existing at liquidation (V1.17.0; mirrors the V1.15.0
// transaction/HR clocks). Privacy-lifecycle events (consent pulses at
// regime effective dates, retention expiry) may still occur AFTER
// this date: the archive has a legal afterlife, the storefront does
// not.
var SignupCutoff = BusinessClosureDate

// InternationalMarketEntry is the earliest signup date for non-US
// customers whose home country isn't overridden below (most of the
// world: international expansion begins 1999).
var InternationalMarketEntry = time.Date(1999, time.January, 1, 0, 0, 0, 0, time.UTC)

// MarketEntryByCountry overrides the default per-country entry date
// for specific markets that opened before the main international push.
// UK was NGE's first non-US market — a 10-year-anniversary flagship
// opened on 1996-06-01 (exactly 10 years after the US founding). All
// other non-US countries default to InternationalMarketEntry (1999).
var MarketEntryByCountry = map[string]time.Time{
	"US": USFirstEraStart,                                      // 1986-06-01 founding
	"GB": time.Date(1996, time.June, 1, 0, 0, 0, 0, time.UTC),   // 10-year anniversary
}

// MarketEntry returns the earliest date NGE operated in the given
// country. US → founding. GB → 10-year-anniversary entry. Other
// non-US → InternationalMarketEntry (1999).
func MarketEntry(country string) time.Time {
	if t, ok := MarketEntryByCountry[country]; ok {
		return t
	}
	return InternationalMarketEntry
}

// --------------------------------------------------------------------
// §9.10.2 — timestamp precision
// --------------------------------------------------------------------

// SignupPrecisionForYear returns the timestamp-precision enum value
// that should be recorded on a customer `signed_up_at` whose original
// capture fell in the given year, per §9.10.2's migration-fingerprint
// ladder.
func SignupPrecisionForYear(year int) string {
	switch {
	case year <= 1990:
		return "year"
	case year <= 1998:
		return "month"
	case year <= 2003:
		return "day"
	case year <= 2007:
		return "hour"
	case year <= 2015:
		return "second"
	default:
		return "millisecond"
	}
}

// --------------------------------------------------------------------
// §9.11 — HR domain
// --------------------------------------------------------------------

// Department — 12 domains the company operates. Order is load-bearing
// for deterministic iteration.
type Department struct {
	ID   int
	Code string
	Name string
}

var Departments = []Department{
	{1, "RETAIL", "Retail"},
	{2, "HO", "Head Office"},
	{3, "FIN", "Finance"},
	{4, "HR", "Human Resources"},
	{5, "IT", "Engineering / IT"},
	{6, "MKT", "Marketing"},
	{7, "BUY", "Buying & Merchandising"},
	{8, "DIST", "Distribution"},
	{9, "CS", "Customer Service"},
	{10, "LP", "Loss Prevention"},
	{11, "RE", "Real Estate"},
	{12, "TRN", "Training"},
}

// PayGrade — G1 (entry) through G10 (executive).
type PayGrade struct {
	ID          int
	Code        string
	Description string
}

var PayGrades = []PayGrade{
	{1, "G1", "Associate"},
	{2, "G2", "Senior Associate"},
	{3, "G3", "Team Lead"},
	{4, "G4", "Assistant Manager"},
	{5, "G5", "Manager"},
	{6, "G6", "Senior Manager"},
	{7, "G7", "Director"},
	{8, "G8", "Senior Director"},
	{9, "G9", "Vice President"},
	{10, "G10", "Executive"},
}

// Role — job title with department anchor + default pay grade.
type Role struct {
	ID             int
	Code           string
	Name           string
	DepartmentID   int
	IsRetailStaff  bool
	DefaultGradeID int
}

var Roles = []Role{
	// Retail (dept 1) — assigned to shops
	{1, "SA", "Sales Associate", 1, true, 1},
	{2, "AM", "Assistant Manager", 1, true, 4},
	{3, "SM", "Store Manager", 1, true, 5},
	// Finance (3)
	{4, "FIN_ANALYST", "Financial Analyst", 3, false, 2},
	{5, "FIN_MGR", "Finance Manager", 3, false, 5},
	// HR (4)
	{6, "HR_BP", "HR Business Partner", 4, false, 5},
	{7, "RECRUITER", "Recruiter", 4, false, 3},
	// IT (5)
	{8, "ENG", "Software Engineer", 5, false, 4},
	{9, "SR_ENG", "Senior Engineer", 5, false, 6},
	{10, "ENG_MGR", "Engineering Manager", 5, false, 7},
	// Marketing (6)
	{11, "MKT_COORD", "Marketing Coordinator", 6, false, 3},
	{12, "MKT_MGR", "Marketing Manager", 6, false, 5},
	// Buying (7)
	{13, "BUYER", "Buyer", 7, false, 5},
	{14, "SR_BUYER", "Senior Buyer", 7, false, 6},
	// Distribution (8)
	{15, "WAREHOUSE", "Warehouse Associate", 8, false, 1},
	{16, "LOG_COORD", "Logistics Coordinator", 8, false, 3},
	// Customer Service (9)
	{17, "CSR", "Customer Service Rep", 9, false, 1},
	{18, "CS_LEAD", "CS Team Lead", 9, false, 4},
	// Executive office (Head Office, dept 2) — V1.16.2 founder roles.
	{19, "CEO", "Founder & Chief Executive", 2, false, 10},
	{20, "COFOUNDER", "Co-Founder & Chair", 2, false, 10},
	// V1.23.0 — the hired exec cast (lore §3). Distinct from the founder
	// roles (19/20) so IsFounderRole stays false and they draw FULL
	// grade-10 executive comp, not the gentler founder draw. Sequential
	// and overlapping tenures are seeded in hr.emitExecs (person_id 3-9).
	{21, "HIRED_CEO", "Chief Executive Officer", 2, false, 10},  // Chip, Brock, Pyle, Priscilla
	{22, "CFO", "Chief Financial Officer", 3, false, 10},        // Voss
	{23, "EVP_TRADE", "EVP Pre-Owned & Trade", 7, false, 9},     // Wickersham
	{24, "WEBMASTER", "Webmaster", 5, false, 4},                 // Todd Macy
	// V1.27 — IT ladder (dept 5) that matured over time, plus facilities
	// roles. Era-gated by HQRoleWeightsForYear so a DBA/QA/Architect never
	// predates the function existing (helpdesk/support ~1995, DBA/dev ~1998,
	// QA ~2000, architecture 2005+). Facilities roles run in every era.
	{25, "HELPDESK", "Helpdesk Analyst", 5, false, 2},
	{26, "SR_HELPDESK", "Senior Helpdesk Analyst", 5, false, 4},
	{27, "WORKPLACE", "Workplace Support Analyst", 5, false, 2},
	{28, "SR_WORKPLACE", "Senior Workplace Support Analyst", 5, false, 4},
	{29, "DBA", "Database Administrator", 5, false, 5},
	{30, "SR_DBA", "Senior Database Administrator", 5, false, 6},
	{31, "DEV", "Software Developer", 5, false, 4},
	{32, "SR_DEV", "Senior Software Developer", 5, false, 6},
	{33, "ARCHITECT", "Software Architect", 5, false, 7},
	{34, "QA", "QA Tester", 5, false, 2},
	{35, "SR_QA", "Senior QA Tester", 5, false, 4},
	{36, "JANITOR", "Janitor", 2, false, 1},            // facilities (Head Office)
	{37, "SECURITY", "Security Officer", 10, false, 1}, // Loss Prevention
	{38, "RECEPTIONIST", "Receptionist", 2, false, 1},  // Head Office
}

// PerShopHeadcount — V1.9.0 deprecated as a fixed constant. Kept as
// a baseline reference for callers that need a "typical" shop size
// (e.g. HQ-staff-ratio calculations against the network). Per-shop
// headcount is now computed by HeadcountForShop() based on shop age.
const PerShopHeadcount = 10

// HeadcountForShop returns the present-day staff count for a shop
// given how long it's been open at as_of. V1.9.0 replaced the V1-V1.8
// uniform PerShopHeadcount=10 with an age-driven log curve.
//
// V1.19 lifts the curve (base 3.0→4.5, coefficient 2.5→3.0) so the
// realised concurrent staffing lands in the realistic 5-7/shop band a
// specialty-games chain actually ran (mostly part-time). The pre-V1.19
// data hid TWO compensating errors — per-person pay ~2× real but only
// ~3.7 staff/shop — which together produced a believable aggregate
// wages-of-revenue (~11%). V1.19 makes per-person pay realistic (lower),
// so headcount must rise to keep the aggregate — and the death-spiral
// curve that depends on it — realistic. The exact constants are
// calibrated empirically against finance.monthly_summary wages% on a
// rebuilt tier; tune here if that curve drifts off ~11-15% (healthy) →
// >100% (2016).
//
//   ~6-7 staff in year 1 (founding crew)
//   ~9-10 by year 5
//   ~12-13 by year 15
//   ~14-15 by year 30
//
// Variance ±2 from the curve, clamped to [3, 25]. Volume-multiplier
// coupling (busier shops carry more staff) is a future refinement;
// right now headcount is purely time-based.
func HeadcountForShop(shopOpen, asOf time.Time, r *rand.Rand) int {
	years := asOf.Sub(shopOpen).Hours() / (24 * 365.25)
	if years < 0 {
		years = 0
	}
	base := 4.5 + 3.0*math.Log1p(years)
	variance := r.Float64()*4.0 - 2.0
	n := int(math.Round(base + variance))
	if n < 3 {
		n = 3
	}
	if n > 25 {
		n = 25
	}
	return n
}

// WageInflationFactor returns the multiplier to apply to USD-baseline
// wage rates (which represent 2025 levels) for a given year and
// country. V1.9.0 fixes V1.7/V1.8 wages being flat across years —
// 1990 wages now correctly scale down to ~50% of 2025 wages for the
// US, JP stays nearly flat (lost decades / no inflation), Brazil
// drops more aggressively (high historical inflation = low 1986
// real wages).
//
// Future-year multipliers > 1.0 are clamped at 2025 for as_of dates
// in the future.
func WageInflationFactor(country string, year int) float64 {
	rate := wageInflationRate(country)
	if year > 2025 {
		year = 2025
	}
	return math.Pow(1.0+rate, float64(year-2025))
}

// wageInflationRate is the country-level annual wage-inflation rate
// used to compute WageInflationFactor. Numbers are rough proxies for
// CPI / nominal-wage growth over the 1986-2025 window.
func wageInflationRate(country string) float64 {
	switch country {
	case "US", "CA", "AU", "GB":
		return 0.025
	case "DE", "FR", "IT", "ES", "NL":
		return 0.020
	case "JP":
		return 0.005 // lost decades — flat
	case "KR":
		return 0.035
	case "BR":
		return 0.060 // high inflation history
	case "PL", "CZ":
		return 0.040
	case "CH":
		return 0.010 // very low inflation
	case "SE", "NO", "DK":
		return 0.018
	default:
		return 0.025
	}
}

// HQStaffRatio — non-shop staff as a fraction of retail headcount.
// HQ / corporate / distribution / customer-service folks sit at
// this fraction above the retail base.
const HQStaffRatio = 0.10

// HQRoleWeight — non-retail role distribution. Weights sum to 1.0.
// Order is load-bearing; weighted sampling walks this slice.
type HQRoleWeight struct {
	RoleID int
	Weight float64
}

// HQRoleWeights — V1.9.0 splits this into era-aware buckets via
// HQRoleWeightsForYear(). The flat slice below remains as a default /
// fallback (representing the modern 2010+ HQ mix).
var HQRoleWeights = HQRoleWeightsForYear(2025)

// HQRoleWeightsForYear returns the era-appropriate HQ role mix. V1.7
// allowed Software Engineers / Engineering Managers in NGE's 1986
// HQ, which is anachronistic (consumer retail SWE roles didn't really
// exist until ~1995-2000). V1.9 era-gates engineering roles and grows
// the marketing-tech / digital roles over time.
//
// Buckets:
//   - 1986-1994: paper-era HQ — Finance, Buyer, Warehouse, CS only
//   - 1995-2004: IT roles emerge (single SWE per HQ, no Eng Mgr yet)
//   - 2005-2014: Engineering scaled, Marketing function grows
//   - 2015+: full modern HQ mix
func HQRoleWeightsForYear(year int) []HQRoleWeight {
	switch {
	case year <= 1994:
		// Pre-Internet HQ: no software/engineering function. Facilities
		// (janitor/security/receptionist) run from the founding.
		return []HQRoleWeight{
			{4, 0.25},  // Financial Analyst
			{5, 0.06},  // Finance Manager
			{6, 0.08},  // HR Business Partner
			{7, 0.04},  // Recruiter
			{11, 0.06}, // Marketing Coordinator
			{12, 0.03}, // Marketing Manager
			{13, 0.14}, // Buyer
			{14, 0.06}, // Senior Buyer
			{15, 0.20}, // Warehouse Associate
			{16, 0.06}, // Logistics Coordinator
			{36, 0.05}, // Janitor
			{37, 0.03}, // Security Officer
			{38, 0.04}, // Receptionist
		}
	case year <= 2004:
		// Web era. IT footprint emerges as the granular support/dev ladder
		// (helpdesk, workplace support, DBA, developer, QA), not a single SWE.
		return []HQRoleWeight{
			{4, 0.20},  // Financial Analyst
			{5, 0.05},  // Finance Manager
			{6, 0.07},  // HR Business Partner
			{7, 0.04},  // Recruiter
			{8, 0.03},  // Software Engineer (generalist)
			{25, 0.03}, // Helpdesk Analyst
			{27, 0.02}, // Workplace Support Analyst
			{29, 0.02}, // Database Administrator
			{31, 0.03}, // Software Developer
			{34, 0.02}, // QA Tester
			{11, 0.08}, // Marketing Coordinator
			{12, 0.03}, // Marketing Manager
			{13, 0.10}, // Buyer
			{14, 0.05}, // Senior Buyer
			{15, 0.16}, // Warehouse Associate
			{16, 0.06}, // Logistics Coordinator
			{17, 0.05}, // Customer Service Rep
			{18, 0.02}, // CS Team Lead
			{36, 0.04}, // Janitor
			{37, 0.02}, // Security Officer
			{38, 0.03}, // Receptionist
		}
	case year <= 2014:
		// IT function scales (e-commerce + mobile): senior specialisms and
		// architecture come online.
		return []HQRoleWeight{
			{4, 0.18},  // Financial Analyst
			{5, 0.04},  // Finance Manager
			{6, 0.06},  // HR Business Partner
			{7, 0.03},  // Recruiter
			{8, 0.03},  // Software Engineer (generalist)
			{25, 0.02}, // Helpdesk Analyst
			{26, 0.01}, // Senior Helpdesk Analyst
			{27, 0.02}, // Workplace Support Analyst
			{28, 0.01}, // Senior Workplace Support Analyst
			{29, 0.02}, // Database Administrator
			{30, 0.01}, // Senior DBA
			{31, 0.03}, // Software Developer
			{32, 0.01}, // Senior Software Developer
			{33, 0.01}, // Software Architect
			{34, 0.02}, // QA Tester
			{35, 0.01}, // Senior QA Tester
			{10, 0.01}, // Engineering Manager
			{11, 0.08}, // Marketing Coordinator
			{12, 0.03}, // Marketing Manager
			{13, 0.08}, // Buyer
			{14, 0.04}, // Senior Buyer
			{15, 0.12}, // Warehouse Associate
			{16, 0.05}, // Logistics Coordinator
			{17, 0.07}, // Customer Service Rep
			{18, 0.02}, // CS Team Lead
			{36, 0.03}, // Janitor
			{37, 0.02}, // Security Officer
			{38, 0.02}, // Receptionist
		}
	default:
		// 2015+: modern HQ mix.
		return []HQRoleWeight{
			{4, 0.17},  // Financial Analyst
			{5, 0.04},  // Finance Manager
			{6, 0.05},  // HR Business Partner
			{7, 0.03},  // Recruiter
			{8, 0.03},  // Software Engineer (generalist)
			{25, 0.02}, // Helpdesk Analyst
			{26, 0.01}, // Senior Helpdesk Analyst
			{27, 0.02}, // Workplace Support Analyst
			{28, 0.01}, // Senior Workplace Support Analyst
			{29, 0.02}, // Database Administrator
			{30, 0.01}, // Senior DBA
			{31, 0.04}, // Software Developer
			{32, 0.02}, // Senior Software Developer
			{33, 0.02}, // Software Architect
			{34, 0.03}, // QA Tester
			{35, 0.01}, // Senior QA Tester
			{10, 0.02}, // Engineering Manager
			{11, 0.08}, // Marketing Coordinator
			{12, 0.03}, // Marketing Manager
			{13, 0.08}, // Buyer
			{14, 0.04}, // Senior Buyer
			{15, 0.11}, // Warehouse Associate
			{16, 0.04}, // Logistics Coordinator
			{17, 0.05}, // Customer Service Rep
			{18, 0.02}, // CS Team Lead
			{36, 0.03}, // Janitor
			{37, 0.02}, // Security Officer
			{38, 0.02}, // Receptionist
		}
	}
}

// HRSourceSystemForYear returns the HR source-system tag per §9.11.5.
func HRSourceSystemForYear(year int) string {
	switch {
	case year <= 1998:
		return "hr_paper_1986_1998"
	case year <= 2013:
		return "hr_legacy_hris_1999_2013"
	default:
		return "hr_unified_2014_plus"
	}
}

// RoleByID returns the Role with the given ID, or a zero Role if not
// found. Small set, linear scan is fine.
func RoleByID(id int) Role {
	for _, r := range Roles {
		if r.ID == id {
			return r
		}
	}
	return Role{}
}

// --------------------------------------------------------------------
// V1.24.0 — the fraud pocket (Track 800, Exhibit H: "The Protégé")
// --------------------------------------------------------------------
//
// One deterministic, statistically discoverable embezzlement woven into
// the data: the store manager of ONE shop issues fraudulent keyed cash
// refunds against stale sales for eighteen months and skims the drawer.
// Canon: a Voss protégé who learned the lesson of 2003 — smaller,
// quieter — and was never caught. The archive catches him.
//
// Signatures (all queryable, none individually damning — the lab's
// point is assembling them):
//   - the shop's refund rate runs ~3x the network during the window
//   - fraud refunds are ALWAYS cash, regardless of the original tender
//   - they reference sales 20-120 days old (organic returns: 1-21 days)
//   - they cluster in the closing hour (21:00-22:00)
//   - they have NO matching 'return' inventory movement (no goods came
//     back — there were no goods)
//   - as manager-KEYED refunds they carry the authorizing spell_id in
//     transactions.staff_id; organic counter returns carry NULL
//
// Sizing: ~5-6 extra refunds/day × ~18 months ≈ 2.5-3k refunds, tens of
// dollars each — ≈$100K skimmed. Material to the man, immaterial to the
// P&L: no calibration impact at any tier.
//
// The fraud shop is addressed by SHOP CODE, not shop_id, so it exists
// at every tier (US-0009 is the ninth US-first-era shop; even the 3g
// tier has ten US shops). All fraud RNG lives in its own namespace
// ("tx/shop/<id>/fraud") — existing streams are untouched.
const (
	FraudShopCode       = "US-0009"
	FraudManagerSpellID = 10 // person_id 10 / spell_id 10: seeded right after the execs (hr.emitFraudManager)

	FraudRefundsPerDayMean = 5.5  // Poisson mean, extra keyed refunds per day in-window
	FraudMaxRefundUSD      = 55.0 // targets sales just under a $60 "manager review" threshold
	FraudLagDaysMin        = 20   // fraud refunds reference stale sales...
	FraudLagDaysMax        = 120  // ...old enough that nobody remembers the customer
	FraudHourOfDay         = 21   // keyed after close, 21:00-22:00
)

// Fraud window: calendar 2006 through mid-2007 — deep in the boom,
// where growth camouflaged everything.
var (
	FraudWindowStart = time.Date(2006, time.January, 1, 0, 0, 0, 0, time.UTC)
	FraudWindowEnd   = time.Date(2007, time.June, 30, 0, 0, 0, 0, time.UTC)
)

// CanonAnomalies reports whether the planted, canon-bound forensic anomalies
// (the US-0009 fraud pocket + its emitFraudManager staff spell) should be
// generated for a given tier. They are enabled only on the lore-faithful
// large tiers (300g / 3t). The small tiers (3g / 30g, product-named "8 GB" /
// "40 GB") are deliberately NON-CANON, generic-retailer datasets: the planted
// crimes don't scale below 300g (see NGE_TIER_COMPARISON.md) and their
// deposition labs are not shipped, so they are stripped there. The fraud
// pocket and its manager are gated TOGETHER on this one predicate, so a keyed
// refund never references a staff spell that wasn't emitted.
func CanonAnomalies(tier string) bool {
	switch tier {
	case "300g", "3t":
		return true
	default: // 3g, 30g, and anything unrecognised → non-canon
		return false
	}
}

// CanonicalMedia returns the standard physical distribution medium for a
// release, derived from its platform (and, for the two PC families that
// genuinely spanned a format boundary, its release year).
//
// V1.23.0: this REPLACES the scraped dbo.releases.media_type. That column
// was a Wikidata "media/format" dump riddled with howlers — a downloadable
// N64 Majora's Mask, a GD-ROM Nintendo 64 title, "video on demand", "vinyl
// record", "cloud computing", plus huge swaths of NULL — because the source
// property conflates a work's every format across every re-release. Games
// shipped on exactly one medium per platform (cartridge / CD-ROM / DVD /
// GD-ROM / …), so we derive that authoritatively. media_type is cosmetic to
// generation (nothing samples on it), so this only cleans the releases
// table — it perturbs no RNG and no calibration.
//
// Platform names match dbo.platforms.name exactly. Every platform maps to a
// single medium except DOS and Microsoft Windows, whose catalogues crossed a
// real media boundary (floppy→CD-ROM, CD-ROM→DVD-ROM); those split on year.
// An unrecognised platform returns "" (→ NULL) rather than a guess.
func CanonicalMedia(platform string, year int) string {
	switch platform {
	// Cartridge platforms (home consoles + most handhelds).
	case "Atari 2600", "Intellivision", "ColecoVision", "MSX",
		"Nintendo Entertainment System", "Sega Master System", "Atari 7800",
		"Sega Genesis", "Atari Lynx", "Game Boy", "Game Gear", "Neo Geo",
		"Super Nintendo Entertainment System", "Atari Jaguar", "Sega 32X",
		"Virtual Boy", "Nintendo 64", "Game Boy Color", "Game Boy Advance":
		return "Cartridge"
	case "TurboGrafx-16":
		return "HuCard"
	case "Nintendo DS":
		return "DS Game Card"
	case "Nintendo 3DS":
		return "3DS Game Card"
	case "PlayStation Vita":
		return "PlayStation Vita Card"

	// Magnetic media (home computers).
	case "Apple II", "Commodore Amiga":
		return "Floppy Disk"
	case "Commodore 64", "ZX Spectrum":
		return "Cassette"

	// Optical media.
	case "Philips CD-i", "Sega CD", "3DO Interactive Multiplayer",
		"Amiga CD32", "PlayStation", "Sega Saturn":
		return "CD-ROM"
	case "Sega Dreamcast":
		return "GD-ROM"
	case "PlayStation 2", "Xbox", "Xbox 360":
		return "DVD-ROM"
	case "PlayStation 3", "PlayStation 4", "Xbox One":
		return "Blu-ray Disc"
	case "PlayStation Portable":
		return "UMD"
	case "Nintendo GameCube":
		return "GameCube Game Disc"
	case "Nintendo Wii":
		return "Wii Optical Disc"
	case "Nintendo Wii U":
		return "Wii U Optical Disc"

	// Era-split PC families — the one place a platform legitimately spans
	// two physical formats, cut at the year the newer drive went mainstream.
	case "DOS":
		if year >= 1994 {
			return "CD-ROM"
		}
		return "Floppy Disk"
	case "Microsoft Windows":
		if year >= 2003 {
			return "DVD-ROM"
		}
		return "CD-ROM"
	}
	return ""
}

// --------------------------------------------------------------------
// §9.13 / §9.15.5 — transaction volume
// --------------------------------------------------------------------

// DailyTxPerShopMedian returns the median transactions/day for a shop
// in the given year, per §9.15.5. Per-shop variance comes from
// volume_multiplier (log-normal σ=0.5) applied on top.
//
// V1.15.0 raised these from ~40% to ~62% of the §9.15.5 raw medians.
// V1.15's decline taper + 2016-09-30 cutoff dropped total transactions
// 37% vs V1.14.6 (200M → 126M at 300g, DB size 300GB → 208GB); the
// 55% per-shop lift restores the database-size-to-tier-label calibration
// without inflating the headline closure-curve / monthly-P&L narrative
// numbers, which scale identically with volume.
// V1.16.5: early-era per-shop volume raised to make NGE's 1986-2000
// boom years profitable — both a lore fix (a bootstrapped game shop
// funded by grandma Jean's small loan should be scrappy and
// profitable, not loss-making for 15 years) and a realism fix (the
// NES/SNES/PS1 boom was peak foot-traffic for specialty game retail;
// the old 13-37 tx/day undersold exactly the era that should be
// healthiest). The old values made per-shop revenue ~$85-490K/yr
// (loss-making against minimal staffing); profitability only switched
// on at the 2000→2001 rate cliff (37→78). New values give early shops
// ~$500-900K/yr and ramp smoothly 45→70→78 into the mature peak.
//   pre-1990: 13 → 45   1991-2000: 37 → 70   (2001+ unchanged)
//
// V1.20: every median is scaled by priceVolumeRebalance (≈0.690) to
// offset the ~1.45× lift in average transaction value (BasePriceByCurrency
// + AgeDiscount + EraPriceFactor, plus the era-varying basket), keeping
// per-shop revenue — and therefore the wages%-of-revenue death-spiral —
// calibrated. Net effect: realistic prices, ~31% fewer (but pricier)
// transactions/day (~78→~54 at peak, closer to a real game store's
// footfall), and a smaller DB (~3T→~2.1T) that leaves headroom for
// future data types (reviews, etc.). Tuned empirically against the 3g
// emit so total revenue matches the pre-V1.20 baseline (±0.1%).
const priceVolumeRebalance = 0.690

// V1.21.0 — software-volume carve-out. NGE's ~$1.34M/shop revenue was
// calibrated to GameStop's ALL-IN figure (which includes hardware), but
// pre-V1.21 NGE sold software only. Adding console/hardware sales would
// over-shoot that target, so software volume is carved DOWN so that
// software (~62-65%) + hardware (~35%) sums back to the same per-shop
// total — keeping the wages%-of-revenue death-spiral calibrated. Tuned
// empirically at the 3g emit alongside HardwareUnitsPerShopDay (see the
// hardware section below). Attach games (sold inside hardware baskets)
// also count as software revenue, so the net carve-out is gentle.
const softwareVolumeCarveout = 0.51

func DailyTxPerShopMedian(year int) float64 {
	var base float64
	switch {
	case year <= 1990:
		base = 45
	case year <= 2000:
		base = 70
	case year <= 2010:
		base = 78
	case year <= 2020:
		base = 62
	default:
		base = 44
	}
	return base * priceVolumeRebalance * softwareVolumeCarveout
}

// --------------------------------------------------------------------
// V1.21.0 — console / hardware sales
//
// Hardware sells OPPOSITE to games: a console spikes at launch then
// declines, is sold near cost (razor-and-blade), drops in price down its
// revision chain, and pulls games into the basket (attach rate). These
// era curves drive internal/transactions/hardware_demand.go; the rand /
// rounding samplers live there alongside the game equivalents.
// --------------------------------------------------------------------

// hardwareVolumeScale is the empirical multiplier on the era hardware
// volume, tuned at 3g so hardware lands ≈ 35% of revenue (the carve-out
// partner of softwareVolumeCarveout).
const hardwareVolumeScale = 2.0

// HardwareUnitsPerShopDay is the expected TOTAL hardware units sold per
// shop per day in a year (the per-platform launch shapes then redistribute
// these across whichever consoles are hot). Grows into the console-heavy
// 2000s, tapers in the decline. Tuned via hardwareVolumeScale at 3g.
func HardwareUnitsPerShopDay(year int) float64 {
	var base float64
	switch {
	case year < 1986:
		base = 0
	case year <= 1990:
		base = 1.0
	case year <= 2000:
		base = 2.0
	case year <= 2010:
		base = 3.5
	case year <= 2016:
		base = 2.5
	default:
		base = 0
	}
	return base * hardwareVolumeScale
}

// HardwareLaunchShape weights a platform's hardware demand by years since
// its launch — the launch SPIKE (years 0-3) then decline. This is what
// concentrates PS2 hardware in 2000-2003 rather than spreading it evenly.
func HardwareLaunchShape(yearsSinceLaunch int) float64 {
	switch {
	case yearsSinceLaunch < 0:
		return 0 // not launched yet
	case yearsSinceLaunch <= 1:
		return 1.00 // launch + year 1: peak
	case yearsSinceLaunch == 2:
		return 0.80
	case yearsSinceLaunch == 3:
		return 0.55
	case yearsSinceLaunch <= 5:
		return 0.30
	case yearsSinceLaunch <= 8:
		return 0.12
	default:
		return 0.04 // long tail / late-life clearance
	}
}

// HardwareModelShape favours the newest available revision of a platform
// (a slim supersedes the fat once it ships). Weighted by years since THAT
// model's own launch.
func HardwareModelShape(yearsSinceModelLaunch int) float64 {
	switch {
	case yearsSinceModelLaunch < 0:
		return 0
	case yearsSinceModelLaunch <= 2:
		return 1.00
	case yearsSinceModelLaunch <= 4:
		return 0.70
	case yearsSinceModelLaunch <= 7:
		return 0.40
	default:
		return 0.20
	}
}

// HardwarePriceDropCurve is the fraction of launch price a model still
// commands after N years on the market ($299 → $199 → $99).
func HardwarePriceDropCurve(yearsOnMarket int) float64 {
	switch {
	case yearsOnMarket <= 0:
		return 1.00
	case yearsOnMarket == 1:
		return 0.85
	case yearsOnMarket == 2:
		return 0.70
	case yearsOnMarket == 3:
		return 0.55
	case yearsOnMarket <= 5:
		return 0.40
	default:
		return 0.33
	}
}

// HardwareNewVsUsedSplit is the probability a hardware sale is NEW (vs a
// used/pre-owned console). Consoles hold "new" longer than games — used
// hardware grows but never dominates the way used games do.
func HardwareNewVsUsedSplit(year int) float64 {
	switch {
	case year <= 1995:
		return 0.98
	case year <= 2005:
		return 0.85
	case year <= 2012:
		return 0.70
	default:
		return 0.60
	}
}

// HardwareCOGSRatio — razor-and-blade: consoles sell near/below cost, so
// hardware carries a much thinner margin than software.
func HardwareCOGSRatio(year int) float64 {
	return 0.95
}

// HardwareTradeInProbability — chance a console purchase is blended with a
// used-console trade-in (the upgrade cycle: trade the old box toward the
// new one). Grows with pre-owned culture, like game trade-ins.
func HardwareTradeInProbability(year int) float64 {
	switch {
	case year <= 1995:
		return 0.03
	case year <= 2005:
		return 0.10
	case year <= 2012:
		return 0.18
	default:
		return 0.22
	}
}

// V1.21.2 — peripherals / accessories. Controllers, memory cards, headsets
// etc. are a real high-margin category in games retail (the profit "blade"
// to the console "razor"). They sell two ways: attached to a console
// purchase (an extra controller / memory card) and standalone (replacements
// for a console the customer already owns).

// accessoryVolumeScale tunes standalone accessory volume; calibrated at 3g.
const accessoryVolumeScale = 6.8

// AccessoryUnitsPerShopDay — standalone accessory purchases per shop-day
// (replacement controllers, memory cards). Smaller than console volume;
// driven off the active-platform installed base.
func AccessoryUnitsPerShopDay(year int) float64 {
	var base float64
	switch {
	case year < 1986:
		base = 0
	case year <= 1990:
		base = 0.5
	case year <= 2000:
		base = 1.2
	case year <= 2010:
		base = 2.2
	case year <= 2016:
		base = 1.6
	default:
		base = 0
	}
	return base * accessoryVolumeScale
}

// AccessoryAttachProbability — chance a console purchase also pulls an
// accessory (extra controller / memory card) into the basket. Rises with
// the multiplayer / online era.
func AccessoryAttachProbability(year int) float64 {
	switch {
	case year <= 1995:
		return 0.25
	case year <= 2005:
		return 0.38
	default:
		return 0.48
	}
}

// --------------------------------------------------------------------
// §9.13.3 — holiday seasonality
// --------------------------------------------------------------------

// SeasonalMultiplier returns the month-of-year multiplier per §9.13.3.
// Jan=0.70, ..., Dec=1.80. Simulator applies this on top of per-year
// base volume.
func SeasonalMultiplier(month time.Month) float64 {
	switch month {
	case time.January:
		return 0.70
	case time.February:
		return 0.75
	case time.March:
		return 0.85
	case time.April:
		return 0.80
	case time.May:
		return 0.85
	case time.June:
		return 0.90
	case time.July:
		return 0.90
	case time.August:
		return 0.95
	case time.September:
		return 1.05
	case time.October:
		return 1.15
	case time.November:
		return 1.55
	case time.December:
		return 1.80
	}
	return 1.0
}

// --------------------------------------------------------------------
// §9.15.6 — channel-mix evolution
// --------------------------------------------------------------------

// ChannelWeight is a normalised share for one channel in one era.
type ChannelWeight struct {
	InStore         float64
	Phone           float64
	Online          float64
	ClickAndCollect float64
	MobileApp       float64
}

// ChannelMixForYear returns the channel shares per §9.15.6.
// Shares always sum to 1.0 for a given year.
//
// V1.9.0 recalibration vs V1.7/V1.8 measurements:
//   - online: was first appearing in 2001 → now 1999 (Amazon/eBay scale)
//   - mobile_app: was first appearing in 2018 → now 2010 (retailer apps)
//   - click_and_collect: was first appearing in 2012 → now 2014 (Walmart
//     pilot 2014, mainstream 2015+)
//   - phone: was disappearing in 2018 → now persists as ~0.5% trace through
//     2025 (phone-orders-for-delivery never died completely)
//
// Buckets reshaped to surface those transitions cleanly.
func ChannelMixForYear(year int) ChannelWeight {
	switch {
	case year <= 1994:
		return ChannelWeight{InStore: 1.00}
	case year <= 1998:
		return ChannelWeight{InStore: 0.93, Phone: 0.07}
	case year <= 2000:
		// 1999-2000: online appears (Amazon, eBay reach scale).
		return ChannelWeight{InStore: 0.90, Phone: 0.06, Online: 0.04}
	case year <= 2007:
		return ChannelWeight{InStore: 0.82, Phone: 0.05, Online: 0.13}
	case year <= 2009:
		return ChannelWeight{InStore: 0.68, Phone: 0.03, Online: 0.29}
	case year <= 2013:
		// 2010+: mobile_app appears (retailer iOS/Android apps).
		return ChannelWeight{InStore: 0.58, Phone: 0.02, Online: 0.36, MobileApp: 0.04}
	case year == 2014:
		// V1.15.0: NGE decline begins — in-store starts collapsing.
		// 2014+: click_and_collect appears (Walmart pilot, then mainstream).
		return ChannelWeight{InStore: 0.45, Phone: 0.01, Online: 0.34, MobileApp: 0.13, ClickAndCollect: 0.07}
	case year == 2015:
		// V1.15.0: restructuring; mall foot-traffic gone.
		return ChannelWeight{InStore: 0.35, Phone: 0.01, Online: 0.39, MobileApp: 0.17, ClickAndCollect: 0.08}
	case year == 2016:
		// V1.15.0: Chapter 11 quarter and fire sale — in-store
		// briefly resurges on clearance days but is overall down.
		return ChannelWeight{InStore: 0.30, Phone: 0.01, Online: 0.40, MobileApp: 0.20, ClickAndCollect: 0.09}
	case year <= 2017:
		// Defensive fallback — NGE has no 2017+ transactions post-V1.15.
		return ChannelWeight{InStore: 0.48, Phone: 0.01, Online: 0.34, MobileApp: 0.12, ClickAndCollect: 0.05}
	case year <= 2020:
		return ChannelWeight{InStore: 0.40, Phone: 0.005, Online: 0.30, MobileApp: 0.195, ClickAndCollect: 0.10}
	case year <= 2023:
		return ChannelWeight{InStore: 0.40, Phone: 0.005, Online: 0.255, MobileApp: 0.225, ClickAndCollect: 0.115}
	default:
		return ChannelWeight{InStore: 0.38, Phone: 0.005, Online: 0.235, MobileApp: 0.25, ClickAndCollect: 0.13}
	}
}

// --------------------------------------------------------------------
// §9.10.4 — payment-method vocabulary by era
// --------------------------------------------------------------------

// PaymentMethodMix returns a weighted list of payment methods valid
// in the given year. Weights sum to 1.0 per year and reflect §9.10.4's
// typical-share column.
type PaymentWeight struct {
	Method string
	Weight float64
}

// PaymentMethodsForYear returns the weighted payment-method slice for
// a given year and channel. Channel filter prevents e.g. `third_party_online`
// on in-store transactions.
//
// V1.9.0: Card-payment evolution recalibrated. The card-method taxonomy
// previously jumped straight from `card_manual` (1986-91) to `card_emv`
// (1992+), but EMV chip-and-PIN didn't exist anywhere until 2006 (UK
// rollout) and not in the US until 2014 (the EMV liability shift).
// V1.9 adds `card_magstripe` covering 1992-2014 (the magnetic-stripe
// swipe era) and reserves `card_emv` for 2006+ globally as a
// simplification (slightly early for US, accurate for EU/UK/AU).
// paymentMethodIntroduced — first year a method can appear. Mirrors
// the `retail.payment_methods.introduced` seed dates. Used by the
// `add` helper below to skip methods that are still in a year-bucket
// list but not yet introduced (e.g. bnpl in the 2015-2019 bucket
// must not appear before 2017).
var paymentMethodIntroduced = map[string]int{
	"cash":                  1986,
	"check":                 1986,
	"card_manual":           1986,
	"card_magstripe":        1992,
	"card_emv":              2006,
	"card_contactless":      2011,
	"mobile_wallet_ios":     2015,
	"mobile_wallet_android": 2016,
	"third_party_online":    2001,
	"bnpl":                  2017,
	"gift_card":             2004,
	"store_credit":          1986,
}

func PaymentMethodsForYear(year int, channel string) []PaymentWeight {
	onlineChannel := channel == "online" || channel == "mobile_app"
	inStoreChannel := channel == "in_store" || channel == "click_and_collect"

	weights := []PaymentWeight{}
	add := func(m string, w float64) {
		// V1.10: skip if the method isn't yet introduced. Year buckets
		// below span ranges (e.g. 2015-2019) where some methods become
		// valid mid-bucket.
		if intro, ok := paymentMethodIntroduced[m]; ok && year < intro {
			return
		}
		weights = append(weights, PaymentWeight{m, w})
	}

	switch {
	case year <= 1994:
		// Pre-magstripe: cash dominates, card_manual is carbon-paper imprints.
		add("cash", 0.90)
		add("check", 0.08)
		add("card_manual", 0.02)
	case year <= 1997:
		// 1995-1997: magstripe swipe terminals roll out, card_manual fades.
		add("cash", 0.75)
		add("check", 0.10)
		add("card_manual", 0.05)
		add("card_magstripe", 0.10)
	case year <= 2003:
		add("cash", 0.55)
		add("check", 0.08)
		add("card_magstripe", 0.32)
		// V1.17.0: store_credit removed from the V1 payment pool — the
		// ledger that would back redemptions is empty until V1.18 wires
		// grants (audit finding F3). Weight folds into renormalisation.
		if onlineChannel {
			add("third_party_online", 0.10) // weights get renormalised below
		}
	case year <= 2005:
		// 2004-2005: pre-EMV bridge era. Magstripe dominant.
		add("cash", 0.45)
		add("check", 0.05)
		add("card_magstripe", 0.40)
		// V1.17.0: store_credit removed from the V1 payment pool — the
		// ledger that would back redemptions is empty until V1.18 wires
		// grants (audit finding F3). Weight folds into renormalisation.
		add("gift_card", 0.05)
		if onlineChannel {
			add("third_party_online", 0.15)
		}
	case year <= 2010:
		// 2006-2010: EMV chip-and-PIN rolls out in UK / EU / AU; US still
		// magstripe-dominant. Globally we blend the two.
		add("cash", 0.32)
		add("check", 0.02)
		add("card_magstripe", 0.30)
		add("card_emv", 0.25)
		add("gift_card", 0.05)
		// V1.17.0: store_credit removed (see first occurrence above).
		if onlineChannel {
			add("third_party_online", 0.15)
		}
	case year <= 2014:
		// 2011-2014: card_contactless appears (UK 2007 but small share);
		// EMV expanding; magstripe declining as US prepares for 2015 shift.
		add("cash", 0.22)
		add("card_magstripe", 0.15)
		add("card_emv", 0.45)
		add("card_contactless", 0.08)
		add("gift_card", 0.05)
		// V1.17.0: store_credit removed from the V1 payment pool — the
		// ledger that would back redemptions is empty until V1.18 wires
		// grants (audit finding F3). Weight folds into renormalisation.
		if onlineChannel {
			add("third_party_online", 0.20)
		}
	case year <= 2019:
		add("cash", 0.15)
		add("card_emv", 0.35)
		add("card_contactless", 0.30)
		add("mobile_wallet_ios", 0.05)
		add("mobile_wallet_android", 0.03)
		add("gift_card", 0.05)
		// V1.17.0: store_credit removed from the V1 payment pool — the
		// ledger that would back redemptions is empty until V1.18 wires
		// grants (audit finding F3). Weight folds into renormalisation.
		if onlineChannel {
			add("third_party_online", 0.20)
			add("bnpl", 0.05)
		}
	default:
		add("cash", 0.08)
		add("card_emv", 0.15)
		add("card_contactless", 0.45)
		add("mobile_wallet_ios", 0.12)
		add("mobile_wallet_android", 0.08)
		add("gift_card", 0.04)
		// V1.17.0: store_credit removed (see first occurrence above).
		if onlineChannel {
			add("third_party_online", 0.15)
			add("bnpl", 0.08)
		}
	}

	// Online-only methods should not appear on in-store channels.
	if inStoreChannel {
		out := make([]PaymentWeight, 0, len(weights))
		for _, w := range weights {
			if w.Method == "third_party_online" || w.Method == "bnpl" {
				continue
			}
			out = append(out, w)
		}
		weights = out
	}

	// Normalise.
	var sum float64
	for _, w := range weights {
		sum += w.Weight
	}
	if sum > 0 {
		for i := range weights {
			weights[i].Weight /= sum
		}
	}
	return weights
}

// --------------------------------------------------------------------
// Transaction pricing (§9.13 implied, simplified for V1 skeleton)
// --------------------------------------------------------------------

// BasePriceByCurrency returns the per-currency "new release today"
// price — the baseline a modern AAA new-release sells for in the
// shop's currency. Catalog titles discount off this.
//
// Historical era adjustment (games in 1988 sold for less in nominal
// currency than 2020) is handled by EraPriceFactor.
// V1.20: new-release anchor raised ×1.2 (USD $50→$60) so a fresh AAA
// reads at the real ~$60 it sold for through the PS4/Gen-8 era (the
// pre-V1.20 $50 base capped even modern new releases below the real
// price — audit finding #4). Paired with a gentler AgeDiscount and a
// compensating volume reduction (DailyTxPerShopMedian) so per-shop
// revenue — and the wages%-of-revenue death-spiral — stay calibrated.
var BasePriceByCurrency = map[string]float64{
	"USD": 60.00,
	"GBP": 48.00,
	"EUR": 54.00,
	"JPY": 6000.00,
	"AUD": 84.00,
	"CAD": 72.00,
	"BRL": 216.00, // import-duty inflated, realistic for BR
	"KRW": 72000.00,
	"SEK": 600.00,
	"NOK": 660.00,
	"DKK": 420.00,
	"CHF": 72.00,
	"PLN": 240.00,
	"CZK": 1440.00,
}

// OnlineEraCustomerFraction returns the probability that a customer
// who signed up in `year` was acquired online (vs in-store). Online
// customers don't need to live near a physical shop — they can be
// anywhere in their country. Used by the customer generator to choose
// between Index.Sample (shop-proximity-damped) and
// Index.SampleAnywhere (pure population-weighted) when placing the
// customer's address.
//
// Curve calibrated against rough industry timelines:
//
//	1986-1995  → 0%    (no consumer internet; everyone walks in)
//	1996-1999  → 5%    (early dial-up, mostly catalog/phone overlap)
//	2000-2003  → 15%   (broadband emerging, e-commerce taking off)
//	2004-2007  → 30%   (mainstream e-commerce — Amazon-era)
//	2008-2011  → 45%   (smartphone era beginning)
//	2012-2015  → 60%
//	2016-2019  → 70%
//	2020+      → 78%   (post-COVID online-first norm)
func OnlineEraCustomerFraction(year int) float64 {
	switch {
	case year <= 1995:
		return 0.00
	case year <= 1999:
		return 0.05
	case year <= 2003:
		return 0.15
	case year <= 2007:
		return 0.30
	case year <= 2011:
		return 0.45
	case year <= 2015:
		return 0.60
	case year <= 2019:
		return 0.70
	default:
		return 0.78
	}
}

// --------------------------------------------------------------------
// V1.17.0 — fire-sale markdowns (§10.35.2 fix 3)
// --------------------------------------------------------------------

// FireSaleStart is the first day of the court-supervised liquidation
// sales (the fire_sale business milestone, 2016-04-02).
var FireSaleStart = time.Date(2016, time.April, 2, 0, 0, 0, 0, time.UTC)

// FireSaleDiscountRate returns the markdown fraction in effect on the
// given date: 0 before the fire sale, then ramping 50% (April 2016)
// → 90% (September) as the administrator clears stock — matching the
// milestone text "Inventory discounted 50-90%". V1.17.0 puts the
// markdown ON THE RECEIPT (line_discount / discount_total); pre-V1.17
// builds carried the narrative discount nowhere at all (audit finding
// F2b: discount_total was 0.00 on every row ever generated).
//
// Deliberately deterministic by date (no RNG): adding a per-line
// jitter draw here would shift every downstream RNG stream and break
// cross-version comparability for the whole 2016 era.
func FireSaleDiscountRate(d time.Time) float64 {
	if d.Before(FireSaleStart) {
		return 0
	}
	switch d.Month() {
	case time.April:
		return 0.50
	case time.May:
		return 0.55
	case time.June:
		return 0.60
	case time.July:
		return 0.70
	case time.August:
		return 0.80
	default: // September — the final clearance
		return 0.90
	}
}

// EraPriceFactor compresses prices in historical eras — a game in
// 1988 retailed for less in nominal currency than its 2020 equivalent.
//
// V1.20: early-era factors raised (≤1990 0.40→0.58, ≤2000 0.60→0.70) so
// historical new releases read at real nominal prices (an NES game ~$35,
// a SNES/PS1 game ~$42 at the new $60 base) rather than the pre-V1.20
// ~$20-30. The 2001-2016 buckets (the revenue-dominant eras) are
// UNCHANGED, so this barely moves the global price factor / volume
// rebalance — early years are <2% of lifetime revenue.
func EraPriceFactor(year int) float64 {
	switch {
	case year <= 1990:
		return 0.58
	case year <= 2000:
		return 0.70
	case year <= 2010:
		return 0.85
	case year <= 2020:
		return 1.00
	default:
		return 1.05
	}
}

// AgeDiscount reduces price for older catalog titles — a 5-year-old
// game sells for less than a freshly released one.
//
// V1.20: softened (age1 0.70→0.80, age≤3 0.50→0.62, age≤10 0.35→0.42,
// older 0.20→0.28). The pre-V1.20 curve dropped catalogue prices too
// fast, dragging the average "new" unit to ~$28 vs the real ~$45 (audit
// finding #4) — real games held value far better (a 1-year-old AAA was
// still ~$40-48, not $35). Raises the blended unit price; the volume
// rebalance (DailyTxPerShopMedian) keeps total revenue calibrated.
func AgeDiscount(yearsSinceRelease int) float64 {
	switch {
	case yearsSinceRelease <= 0:
		return 1.00
	case yearsSinceRelease == 1:
		return 0.80
	case yearsSinceRelease <= 3:
		return 0.62
	case yearsSinceRelease <= 10:
		return 0.42
	default:
		return 0.28
	}
}

// MinorUnit returns the decimal places (2 for USD, 0 for JPY).
// Pricing quantises to this precision.
func MinorUnit(currency string) int {
	switch currency {
	case "JPY", "KRW":
		return 0
	default:
		return 2
	}
}

// SalesTaxRate returns the sales-tax / VAT / GST rate applied to a sale
// in the given country and year. V1.22.0 replaces the old flat 8% with
// real per-jurisdiction rates that also step over time — UK VAT 15→17.5
// →20, Japan consumption tax 3→5→8, Australia's GST switching on in 2000,
// the German 2007 VAT hike, etc. Great training fodder (tax-by-country,
// tax-rate-change-over-time) and a believability fix (a flat 8% on every
// receipt in every country for 31 years is a synthetic tell). Rates are
// realistic approximations, not a tax-law reference.
func SalesTaxRate(country string, year int) float64 {
	switch country {
	case "US": // state sales tax (no federal); avg crept up over time
		if year <= 2000 {
			return 0.060
		}
		return 0.070
	case "GB": // VAT
		switch {
		case year <= 1990:
			return 0.150
		case year <= 2010:
			return 0.175
		default:
			return 0.200
		}
	case "DE": // VAT (Mehrwertsteuer)
		switch {
		case year <= 1997:
			return 0.150
		case year <= 2006:
			return 0.160
		default:
			return 0.190 // 2007 hike to 19%
		}
	case "FR": // TVA
		if year <= 1999 {
			return 0.186
		} else if year <= 2013 {
			return 0.196
		}
		return 0.200
	case "IT": // IVA
		switch {
		case year <= 1996:
			return 0.190
		case year <= 2012:
			return 0.200
		default:
			return 0.220
		}
	case "ES": // IVA
		switch {
		case year <= 2009:
			return 0.160
		case year <= 2011:
			return 0.180
		default:
			return 0.210
		}
	case "NL": // BTW
		if year <= 2000 {
			return 0.175
		} else if year <= 2011 {
			return 0.190
		}
		return 0.210
	case "JP": // consumption tax
		switch {
		case year <= 1988:
			return 0.000
		case year <= 1996:
			return 0.030
		case year <= 2013:
			return 0.050
		default:
			return 0.080
		}
	case "CA": // GST + average PST, combined
		return 0.120
	case "AU": // GST switched on 2000-07
		if year < 2000 {
			return 0.000
		}
		return 0.100
	case "BR": // ICMS (state-average)
		return 0.170
	case "KR": // VAT
		return 0.100
	case "CH": // VAT (low)
		if year <= 2010 {
			return 0.076
		}
		return 0.080
	case "SE", "NO", "DK": // Nordic VAT
		return 0.250
	case "PL": // VAT
		if year <= 2010 {
			return 0.220
		}
		return 0.230
	case "CZ": // VAT
		if year <= 2012 {
			return 0.200
		}
		return 0.210
	default:
		return 0.100
	}
}

// PromoLineFraction returns the share of (non-fire-sale) sale lines that
// carry a promotional markdown — sale events, bundle/clearance pricing.
// V1.22.0. Grows modestly over time as discount-driven retail intensifies.
func PromoLineFraction(year int) float64 {
	switch {
	case year <= 1995:
		return 0.05
	case year <= 2007:
		return 0.08
	default:
		return 0.11
	}
}

// ReturnProbability returns the chance a sale spawns a later refund/return.
// V1.22.0. Real specialty retail runs ~5-8% returns (defective discs, gift
// returns, buyer's remorse); rises a touch in the online era (mail-order
// returns are easier). The returned receipt refunds the original tender
// (cash/card) — NOT store credit — so the store-credit ledger is untouched.
func ReturnProbability(year int) float64 {
	switch {
	case year <= 2000:
		return 0.05
	case year <= 2010:
		return 0.06
	default:
		return 0.075
	}
}

// --------------------------------------------------------------------
// §9.10.1 — source systems relevant to shop opening metadata
// --------------------------------------------------------------------

// SourceSystemForYear returns the source_system an administrative
// record (a shop, an employment spell, etc.) created in the given year
// would carry, assuming it rode forward through each re-platforming.
func SourceSystemForYear(year int) string {
	switch {
	case year <= 2003:
		return "pos_legacy_1986_2003"
	case year <= 2015:
		return "pos_transitional_2004_2015"
	default:
		return "pos_current"
	}
}

// CustomerLinkageProbability returns the probability that a transaction
// in the given channel and year is associated with a customer record
// (vs being an anonymous walk-in cash sale).
//
// V1.14.0: pre-V1.14, transactions never linked to customers
// (`customer_id` was uniformly NULL even on 2024 online purchases).
// This function fixes that with era- and channel-aware probabilities.
//
// Rationale:
//   - Online / mobile_app / click_and_collect REQUIRE an account → 100%.
//   - Phone orders: pre-1995 catalogue customers were repeat-buyers
//     with files on hand (30%); CRM systems matured through the 90s
//     to ~60% in 1995-1999; modern call-centres always identify (90%).
//   - In-store pre-1995: only store-credit holders, warranty
//     registrants, and cheque/account customers got logged (5%). The
//     vast majority were anonymous cash sales — the most common
//     immersion-breaking artifact pre-V1.14 was 1988 in-store sales
//     all carrying `customer_id`.
//   - In-store 1995-1999: early barcode loyalty cards (10%).
//   - In-store 2000-2009: loyalty cards mainstream (25%).
//   - In-store 2010-2019: payment-card linkage + app-based offers (45%).
//   - In-store 2020+: digital receipts, app integration (60%).
func CustomerLinkageProbability(channel string, year int) float64 {
	switch channel {
	case "online", "mobile_app", "click_and_collect":
		return 1.0
	case "phone":
		switch {
		case year < 1995:
			return 0.30
		case year < 2000:
			return 0.60
		default:
			return 0.90
		}
	case "in_store":
		switch {
		case year < 1995:
			return 0.05
		case year < 2000:
			return 0.10
		case year < 2010:
			return 0.25
		case year < 2020:
			return 0.45
		default:
			return 0.60
		}
	default:
		return 0.0
	}
}

// --------------------------------------------------------------------
// §9.13.x — platform lifecycle (NGE retail availability)
//
// PlatformLifecycle drives transaction-time realism: at any given sale
// year, only platforms that were actively shipping new units (or within
// their typical clearance year) can sell as new; only platforms within
// the current 3 console generations trade as used. Older platforms
// either fall to "vintage / collector" (deferred to V1.2) or simply
// drop out of the regular catalogue.
//
// NGE's corpus deliberately stops at Gen 8 (PS4/Xbox One/Wii U/3DS/Vita
// era) because Wikipedia's modern lists don't separate physical from
// digital releases, and NGE is a physical-media retailer. Consequently
// from ~2023 onwards no new SKUs are sold — NGE pivots to a retro /
// pre-owned focus, mirroring the actual decline of physical games at
// retail.
//
// Generation numbers: 2 = Atari 2600 / Intellivision era, ..., 8 = PS4
// / Xbox One era. Home computers (Apple II, DOS, Windows, ZX, Amiga,
// MSX) carry IsComputer = true and skip the generation-based used
// rule — they have their own decade-long resale tail post-discontinue.
// --------------------------------------------------------------------

// PlatformLifecycle describes the retail-availability window for a
// single platform. ReleasedYear / DiscontinuedYear come from public
// hardware-lifecycle records (Wikipedia / sales-discontinuation press
// releases), trimmed where appropriate to "physical retail end" rather
// than "last unit ever shipped" — e.g. PS2 was sold in JP through 2013.
type PlatformLifecycle struct {
	Family           string // 'Sony' | 'Nintendo' | 'Microsoft' | 'Sega' | 'Atari' | etc.
	Generation       int    // 2-8 for consoles; 0 for computers
	IsComputer       bool   // true for Apple II, DOS, Windows, ZX, Amiga, MSX
	ReleasedYear     int
	DiscontinuedYear int
}

// PlatformLifecycles maps the catalogue's `platform` string (as it
// appears in releases.tsv) to its lifecycle row. Keys must match
// catalogue values exactly — adding a new platform CSV requires adding
// an entry here, otherwise the platform shows up in retail.platforms
// with NULL years and gets skipped by era-aware transaction sampling.
var PlatformLifecycles = map[string]PlatformLifecycle{
	// Gen 2 — second-generation consoles
	"Atari 2600":    {Family: "Atari", Generation: 2, ReleasedYear: 1977, DiscontinuedYear: 1992},
	"Intellivision": {Family: "Mattel", Generation: 2, ReleasedYear: 1979, DiscontinuedYear: 1990},
	"ColecoVision":  {Family: "Coleco", Generation: 2, ReleasedYear: 1982, DiscontinuedYear: 1985},

	// Gen 3 — 8-bit era
	"Atari 7800":                   {Family: "Atari", Generation: 3, ReleasedYear: 1986, DiscontinuedYear: 1992},
	"Nintendo Entertainment System": {Family: "Nintendo", Generation: 3, ReleasedYear: 1983, DiscontinuedYear: 1995},
	"Sega Master System":            {Family: "Sega", Generation: 3, ReleasedYear: 1985, DiscontinuedYear: 1992},

	// Gen 4 — 16-bit era
	"Super Nintendo Entertainment System": {Family: "Nintendo", Generation: 4, ReleasedYear: 1990, DiscontinuedYear: 1999},
	"Sega Genesis":                        {Family: "Sega", Generation: 4, ReleasedYear: 1988, DiscontinuedYear: 1997},
	"TurboGrafx-16":                       {Family: "NEC", Generation: 4, ReleasedYear: 1987, DiscontinuedYear: 1995},
	"Sega CD":                             {Family: "Sega", Generation: 4, ReleasedYear: 1991, DiscontinuedYear: 1996},
	"Sega 32X":                            {Family: "Sega", Generation: 4, ReleasedYear: 1994, DiscontinuedYear: 1996},
	"Game Boy":                            {Family: "Nintendo", Generation: 4, ReleasedYear: 1989, DiscontinuedYear: 2003},
	"Game Gear":                           {Family: "Sega", Generation: 4, ReleasedYear: 1990, DiscontinuedYear: 1997},
	"Atari Lynx":                          {Family: "Atari", Generation: 4, ReleasedYear: 1989, DiscontinuedYear: 1995},
	"Neo Geo":                             {Family: "SNK", Generation: 4, ReleasedYear: 1990, DiscontinuedYear: 2004},
	"Amiga CD32":                          {Family: "Commodore", Generation: 4, ReleasedYear: 1993, DiscontinuedYear: 1996},

	// Gen 5 — 32/64-bit era
	"PlayStation":                  {Family: "Sony", Generation: 5, ReleasedYear: 1994, DiscontinuedYear: 2006},
	"Nintendo 64":                  {Family: "Nintendo", Generation: 5, ReleasedYear: 1996, DiscontinuedYear: 2002},
	"Sega Saturn":                  {Family: "Sega", Generation: 5, ReleasedYear: 1994, DiscontinuedYear: 2000},
	"3DO Interactive Multiplayer":  {Family: "3DO", Generation: 5, ReleasedYear: 1993, DiscontinuedYear: 1996},
	"Atari Jaguar":                 {Family: "Atari", Generation: 5, ReleasedYear: 1993, DiscontinuedYear: 1996},
	"Virtual Boy":                  {Family: "Nintendo", Generation: 5, ReleasedYear: 1995, DiscontinuedYear: 1996},
	"Game Boy Color":               {Family: "Nintendo", Generation: 5, ReleasedYear: 1998, DiscontinuedYear: 2003},
	"Philips CD-i":                 {Family: "Philips", Generation: 5, ReleasedYear: 1991, DiscontinuedYear: 1998},

	// Gen 6 — 128-bit era
	"Sega Dreamcast":     {Family: "Sega", Generation: 6, ReleasedYear: 1998, DiscontinuedYear: 2001},
	"PlayStation 2":      {Family: "Sony", Generation: 6, ReleasedYear: 2000, DiscontinuedYear: 2013},
	"Xbox":               {Family: "Microsoft", Generation: 6, ReleasedYear: 2001, DiscontinuedYear: 2008},
	"Nintendo GameCube":  {Family: "Nintendo", Generation: 6, ReleasedYear: 2001, DiscontinuedYear: 2007},
	"Game Boy Advance":   {Family: "Nintendo", Generation: 6, ReleasedYear: 2001, DiscontinuedYear: 2010},

	// Gen 7
	"Xbox 360":             {Family: "Microsoft", Generation: 7, ReleasedYear: 2005, DiscontinuedYear: 2016},
	"PlayStation 3":        {Family: "Sony", Generation: 7, ReleasedYear: 2006, DiscontinuedYear: 2017},
	"Nintendo Wii":         {Family: "Nintendo", Generation: 7, ReleasedYear: 2006, DiscontinuedYear: 2017},
	"Nintendo DS":          {Family: "Nintendo", Generation: 7, ReleasedYear: 2004, DiscontinuedYear: 2014},
	"PlayStation Portable": {Family: "Sony", Generation: 7, ReleasedYear: 2004, DiscontinuedYear: 2014},

	// Gen 8 — newest tier in the corpus (Switch/PS5/Xbox Series deliberately omitted)
	"Nintendo Wii U":    {Family: "Nintendo", Generation: 8, ReleasedYear: 2012, DiscontinuedYear: 2017},
	"PlayStation 4":     {Family: "Sony", Generation: 8, ReleasedYear: 2013, DiscontinuedYear: 2024},
	"Xbox One":          {Family: "Microsoft", Generation: 8, ReleasedYear: 2013, DiscontinuedYear: 2020},
	"Nintendo 3DS":      {Family: "Nintendo", Generation: 8, ReleasedYear: 2011, DiscontinuedYear: 2020},
	"PlayStation Vita":  {Family: "Sony", Generation: 8, ReleasedYear: 2011, DiscontinuedYear: 2019},

	// Home computers — own retail era, no console-gen rule
	"Apple II":          {Family: "Apple", IsComputer: true, ReleasedYear: 1977, DiscontinuedYear: 1990},
	"DOS":               {Family: "IBM PC", IsComputer: true, ReleasedYear: 1981, DiscontinuedYear: 2000},
	"Microsoft Windows": {Family: "Microsoft", IsComputer: true, ReleasedYear: 1992, DiscontinuedYear: 2018},
	"ZX Spectrum":       {Family: "Sinclair", IsComputer: true, ReleasedYear: 1982, DiscontinuedYear: 1992},
	"Commodore 64":      {Family: "Commodore", IsComputer: true, ReleasedYear: 1982, DiscontinuedYear: 1994},
	"Commodore Amiga":   {Family: "Commodore", IsComputer: true, ReleasedYear: 1985, DiscontinuedYear: 1996},
	"MSX":               {Family: "Various", IsComputer: true, ReleasedYear: 1983, DiscontinuedYear: 1990},
}

// PlatformLifecycleFor returns the lifecycle row for the given catalogue
// `platform` string. Returns the zero value (Family="", Generation=0,
// ReleasedYear=0) for unknown platforms — caller treats as "no lifecycle
// metadata, exclude from era-aware sampling but still loadable".
func PlatformLifecycleFor(name string) PlatformLifecycle {
	return PlatformLifecycles[name]
}

// IsNewAvailable reports whether new units of this platform's titles
// would still be on NGE shelves in `year`. The +1 buffer captures the
// typical clearance / final-stock period after manufacturer support
// formally ends.
func (p PlatformLifecycle) IsNewAvailable(year int) bool {
	if p.ReleasedYear == 0 {
		return false
	}
	return year >= p.ReleasedYear && year <= p.DiscontinuedYear+1
}

// IsUsedAvailable reports whether used copies of this platform's titles
// would still trade through NGE in `year`. For consoles the rule is "in
// the current 3 generations" (current_gen, current_gen-1, current_gen-2);
// older falls to vintage/collector (deferred to V1.2). For computers,
// a flat 10-year resale tail past discontinue.
func (p PlatformLifecycle) IsUsedAvailable(year, currentGen int) bool {
	if p.ReleasedYear == 0 || year < p.ReleasedYear {
		return false
	}
	if p.IsComputer {
		return year <= p.DiscontinuedYear+10
	}
	return p.Generation >= currentGen-2
}

// CurrentGen returns the most recent console generation actively
// shipping new hardware in `year`. Used by IsUsedAvailable to compute
// the "+2 generations back" floor for used-availability.
//
// Hardcoded gen-start years rather than scanning PlatformLifecycles —
// the boundaries are stable industry-consensus dates and the static
// switch is faster + clearer than a max-scan. Caps at 8 because the
// corpus deliberately doesn't include Gen 9 (Switch/PS5/Xbox Series).
func CurrentGen(year int) int {
	switch {
	case year < 1983:
		return 2
	case year < 1987:
		return 3
	case year < 1993:
		return 4
	case year < 1998:
		return 5
	case year < 2005:
		return 6
	case year < 2012:
		return 7
	default:
		return 8
	}
}

// --------------------------------------------------------------------
// Condition vocabulary + new-vs-used split
//
// transaction_lines.condition values must match the schema's existing
// inventory CHECK constraint vocabulary. Only `new` and the four used
// grades are emitted by the simulator; `description` (free-text
// fallback) is always NULL because every line is a catalogue SKU.
// --------------------------------------------------------------------

const (
	ConditionNew       = "new"
	ConditionUsedMint  = "used_mint"
	ConditionUsedGood  = "used_good"
	ConditionUsedFair  = "used_fair"
	ConditionUsedLoose = "used_loose"
)

// newUsedAnchors — V1.20: era anchor points for the new-copy share,
// interpolated smoothly by NewVsUsedSplit. Same endpoints as the
// pre-V1.20 step function; package-level so the hot per-line path
// allocates nothing.
var newUsedAnchors = []struct {
	year  int
	share float64
}{
	{1986, 0.97}, {1990, 0.92}, {1998, 0.80},
	{2007, 0.65}, {2015, 0.50}, {2022, 0.30},
}

// NewVsUsedSplit returns the probability that a transaction line is for
// a new (factory-fresh) copy in the given year. Curve mirrors NGE's
// arc: new-game-only retailer in the 1980s, balanced new+used retailer
// through the physical-media peak, retro / pre-owned specialist after
// 2022 (per the §10.8 Gen 8 cap and physical-only-retailer decision).
//
// V1.20: smoothly INTERPOLATED between era anchors instead of a step
// function (audit #6 — the used mix "stepped in clean plateaus rather
// than drifting"). The realized share now eases a little each year
// (0.65 in 2007 → 0.64 → 0.62 …), as a real retailer's pre-owned
// penetration crept up, rather than snapping 0.65→0.50 at a boundary.
func NewVsUsedSplit(year int) float64 {
	if year <= newUsedAnchors[0].year {
		return newUsedAnchors[0].share
	}
	if year >= 2023 {
		return 0.0 // retro pivot — corpus has no Gen 9 catalogue
	}
	n := len(newUsedAnchors)
	if last := newUsedAnchors[n-1]; year >= last.year {
		return last.share
	}
	for i := 1; i < n; i++ {
		if year <= newUsedAnchors[i].year {
			a, b := newUsedAnchors[i-1], newUsedAnchors[i]
			frac := float64(year-a.year) / float64(b.year-a.year)
			return a.share + (b.share-a.share)*frac
		}
	}
	return newUsedAnchors[n-1].share
}

// ConditionWeight is a cumulative-distribution entry for sampling
// the four used grades. Caller draws u ∈ [0,1) and returns the first
// entry where u < Cum.
type ConditionWeight struct {
	Condition string
	Cum       float64
}

// UsedConditionWeights — distribution of grades on used inflow per
// §9.14.5. The simulator V1 applies these directly to outbound used
// sales too (a simplification: real shops have stale-stock dynamics
// that shift the inflow vs outflow distributions). V1.1+ refinement.
var UsedConditionWeights = []ConditionWeight{
	{ConditionUsedMint, 0.25},
	{ConditionUsedGood, 0.70},
	{ConditionUsedFair, 0.92},
	{ConditionUsedLoose, 1.00},
}

// ConditionPriceMultiplier is the unit-price discount factor relative
// to a new copy. Applied on top of EraPriceFactor × AgeDiscount, so
// "loose" used copies of an old game can land near the floor.
func ConditionPriceMultiplier(condition string) float64 {
	switch condition {
	case ConditionNew:
		return 1.00
	case ConditionUsedMint:
		return 0.85
	case ConditionUsedGood:
		return 0.65
	case ConditionUsedFair:
		return 0.40
	case ConditionUsedLoose:
		return 0.25
	default:
		return 1.00
	}
}

// --------------------------------------------------------------------
// Regional platform bias
//
// Real-world platform popularity varied dramatically by country —
// Amiga was huge in UK/Germany, near-invisible in US/Japan; MSX
// dominated Japan and Spain, never sold in NA; Atari 2600/7800 were
// US/Canada-centric; ZX Spectrum was a UK + Eastern-Bloc phenomenon;
// Master System was massive in Brazil via TecToy clones.
//
// The simulator's catalogue size + decay-curve weighting alone
// can't capture this — Wikipedia catalogue sizes reflect "how
// thoroughly the platform was documented" not "how many units sold
// where." The PlatformRegionBias multiplier corrects platform pick
// for the shop's country at sample time.
//
// Multipliers are relative — 1.0 = average for this platform globally,
// 0.1 = niche in this country, 4.0 = market leader. Missing entries
// default to 1.0 (no regional preference). Globally-consistent
// platforms (Nintendo home consoles, Sony, handhelds) are deliberately
// omitted from the table — they sold roughly evenly worldwide.
// --------------------------------------------------------------------

// PlatformRegionBias maps platform name → ISO country code → multiplier.
// Values are hand-curated from Wikipedia hardware-sales records and
// long-running gaming-press regional sales coverage. Order within
// each inner map doesn't matter (multiplier-lookup, not iteration).
var PlatformRegionBias = map[string]map[string]float64{
	// 80s home computers — strongly regional
	"Commodore Amiga": {
		"GB": 4.0, "DE": 3.5, "FR": 2.5, "IT": 2.0, "ES": 1.5, "NL": 2.5,
		"SE": 2.0, "NO": 2.0, "DK": 2.0, "CH": 1.5, "AU": 1.5,
		"PL": 1.5, "CZ": 1.5,
		"US": 0.1, "CA": 0.1, "JP": 0.05, "KR": 0.05, "BR": 0.5,
	},
	"Commodore 64": {
		// C64 was waning in US by 1990 — Nintendo had eaten the gaming
		// market. EU + Australia kept it alive years longer.
		"GB": 3.0, "DE": 3.0, "AU": 2.0,
		"US": 0.8, "CA": 0.8,
		"JP": 0.1, "KR": 0.1, "BR": 0.7,
	},
	"ZX Spectrum": {
		"GB": 5.0, "ES": 2.0, "PL": 2.5, "CZ": 2.5, "DE": 1.5, "IT": 1.0,
		"US": 0.05, "CA": 0.05, "JP": 0.05, "KR": 0.05, "AU": 0.5, "BR": 0.7,
	},
	"MSX": {
		"JP": 4.0, "KR": 3.0, "ES": 2.0, "NL": 2.0, "BR": 2.5,
		"US": 0.1, "CA": 0.1, "GB": 0.5,
	},
	"Apple II": {
		// Apple II was old tech by 1990 — installed base existed but
		// new game releases tapered off. Less dominant than the catalogue
		// volume suggests for a console-era retailer.
		"US": 1.3, "CA": 1.3, "GB": 0.5, "JP": 0.3,
	},
	"DOS": {
		// PC gaming was real but smaller than console gaming in retail
		// terms through ~1995. Reduce from "PC-leaning" to "balanced".
		"US": 1.0, "CA": 1.0, "GB": 1.2, "DE": 1.2,
		"JP": 0.5, "KR": 0.5, "BR": 0.7,
	},
	"Microsoft Windows": {
		"US": 1.2, "CA": 1.2, "GB": 1.2, "DE": 1.2,
		"JP": 0.5, "KR": 0.5, "BR": 0.7,
	},

	// Atari — heavily US-centric
	"Atari 2600":   {"US": 2.0, "CA": 2.0, "AU": 1.5, "JP": 0.2},
	"Atari 7800":   {"US": 2.0, "CA": 2.0, "JP": 0.1},
	"Atari Lynx":   {"US": 1.5, "CA": 1.5, "JP": 0.2},
	"Atari Jaguar": {"US": 1.5, "CA": 1.5, "JP": 0.2},

	// Mattel/Coleco — North America only really
	"Intellivision": {"US": 2.0, "CA": 2.0, "GB": 0.5, "JP": 0.1},
	"ColecoVision":  {"US": 2.0, "CA": 2.0, "GB": 0.5, "JP": 0.1},

	// Japanese-market platforms with weak Western penetration
	"TurboGrafx-16": {"JP": 4.0, "US": 0.4, "GB": 0.2, "DE": 0.2, "FR": 0.2},
	"Neo Geo":       {"JP": 3.0, "US": 0.5, "GB": 0.3, "BR": 0.3},

	// Sega — heavy regional skew
	"Sega Master System": {
		"BR": 4.0, "GB": 1.5, "AU": 1.5, "DE": 1.2, "FR": 1.2, "IT": 1.0, "ES": 1.0,
		"US": 0.3, "JP": 0.5,
	},
	"Sega Genesis": {
		"US": 1.5, "BR": 3.0, "GB": 1.2, "DE": 1.2, "AU": 1.5, "JP": 0.5,
	},
	"Sega Saturn":   {"JP": 3.0, "US": 0.5, "GB": 0.5, "DE": 0.5, "FR": 0.5, "BR": 0.5},
	"Sega Dreamcast": {"JP": 1.5, "US": 0.8, "GB": 0.8},
	"Game Gear":     {"JP": 1.2, "GB": 1.2, "US": 0.7},
	"Sega CD":       {"JP": 1.5, "US": 0.7, "BR": 0.5},
	"Sega 32X":      {"US": 0.7, "JP": 1.0},

	// Microsoft Xbox — NA-leaning, weak in Japan
	"Xbox":     {"US": 2.0, "CA": 2.0, "GB": 1.5, "AU": 1.5, "JP": 0.2, "KR": 0.2, "BR": 0.5},
	"Xbox 360": {"US": 1.5, "CA": 1.5, "GB": 1.3, "AU": 1.3, "JP": 0.3, "KR": 0.3, "BR": 0.7},
	"Xbox One": {"US": 1.5, "CA": 1.5, "GB": 1.3, "AU": 1.3, "JP": 0.3, "KR": 0.3, "BR": 0.7},

	// Failure platforms — varied where they did/didn't catch on
	"3DO Interactive Multiplayer": {"JP": 1.5, "US": 0.7, "GB": 0.5},
	"Philips CD-i":                {"NL": 1.5, "US": 0.5, "JP": 0.3},
	"Amiga CD32":                  {"GB": 2.0, "DE": 1.5, "US": 0.2, "JP": 0.05},
	"Virtual Boy":                 {"JP": 1.0, "US": 0.5, "GB": 0.05},

	// Nintendo home consoles — strongly NA + JP, weaker in EU where
	// Sega and home computers competed harder during NES/SNES era.
	"Nintendo Entertainment System": {
		"US": 2.5, "CA": 2.5, "JP": 1.5,
		"GB": 0.4, "DE": 0.6, "FR": 0.6, "IT": 0.6, "ES": 0.6, "NL": 0.7, "PL": 0.5,
		"AU": 1.0, "BR": 0.8,
	},
	"Super Nintendo Entertainment System": {
		"US": 1.6, "CA": 1.6, "JP": 1.5,
		"GB": 0.7, "DE": 0.9, "FR": 0.9, "AU": 0.9, "BR": 0.7,
	},
	"Nintendo 64": {
		"US": 1.3, "CA": 1.3, "JP": 0.9,
		"GB": 0.9, "DE": 0.9, "AU": 1.0,
	},
	"Nintendo GameCube": {
		"US": 1.1, "CA": 1.1, "JP": 1.0,
		"GB": 0.9, "BR": 0.5,
	},
	"Nintendo Wii": {
		"US": 1.2, "CA": 1.2, "JP": 1.1,
		"BR": 0.6,
	},
	// Wii U deliberately omitted — flopped roughly evenly worldwide.

	// Nintendo handhelds — Nintendo's most globally consistent line,
	// with a slight JP edge on each generation.
	"Game Boy":         {"US": 1.3, "CA": 1.3, "JP": 1.2},
	"Game Boy Color":   {"US": 1.3, "CA": 1.3, "JP": 1.2},
	"Game Boy Advance": {"US": 1.2, "CA": 1.2, "JP": 1.2},
	"Nintendo DS":      {"US": 1.1, "CA": 1.1, "JP": 1.3},
	"Nintendo 3DS":     {"US": 1.0, "CA": 1.0, "JP": 1.3},

	// Sony platforms — broadly global, with a slight JP edge on the
	// PS1/PS2/PS3 generations and a sharper one on PSP/Vita (handheld
	// gaming was more entrenched in Japan).
	"PlayStation":   {"JP": 1.2, "BR": 0.7},
	"PlayStation 2": {"JP": 1.2, "BR": 0.7},
	"PlayStation 3": {"JP": 1.1, "US": 0.95, "BR": 0.7},
	// PS4 left at defaults — sold roughly even globally.
	"PlayStation Portable": {"JP": 1.5, "US": 0.8, "GB": 0.9, "BR": 0.6},
	"PlayStation Vita":     {"JP": 1.8, "US": 0.6, "GB": 0.7, "DE": 0.7, "BR": 0.5},
}

// RegionBiasFor returns the per-(platform, country) sales-volume
// multiplier, defaulting to 1.0 for unlisted entries (globally-
// consistent platforms or unknown country codes).
func RegionBiasFor(platform, country string) float64 {
	if perCountry, ok := PlatformRegionBias[platform]; ok {
		if mult, ok := perCountry[country]; ok {
			return mult
		}
	}
	return 1.0
}
