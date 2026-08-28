// Package transactions generates sale transactions with nested lines
// and payments, deterministically per §9.10, §9.13, §9.15.
//
// Build-mode only: emits complete transaction records (header + lines
// + payment) one at a time via a callback. The downstream DB writer
// in V1.1 bulk-loads these into retail.transactions /
// retail.transaction_lines / retail.payments.
//
// A real OLTP workload runner — firing live INSERTs at the database
// through a driver, one transaction per worker — is explicitly V1.1
// territory (separate program).
//
// Generation pattern per shop per day:
//  1. Compute expected daily tx count from §9.15.5 median × shop
//     volume_multiplier (log-normal σ=0.5) × §9.13.3 seasonal.
//  2. Poisson-sample actual count.
//  3. For each tx: pick channel (§9.15.6), 1–2 lines (§9.13 uniform
//     V1, reception-weighted V1.1), payment method (§9.10.4).
//
// Current scope is deliberately thin — no trade-ins, no inventory
// movements, no customer linkage, no reception-weighted SKU sampling.
// Each of those is its own V1.1 milestone.
package transactions

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"emporium/internal/catalog"
	"emporium/internal/hardware"
	"emporium/internal/policy"
	"emporium/internal/rng"
	"emporium/internal/shops"
)

// CustomerPicker abstracts customers.Index so this package doesn't pull
// in the heavyweight customers import. Pick returns a customer_id for
// the given country+date, or (0, false) if no eligible customer is
// available (typical at very-early-era dates pre-customer-pool growth).
type CustomerPicker interface {
	Pick(country string, asOf time.Time, r *rand.Rand) (int64, bool)
	// SavedPaymentFor (V1.18.0) returns the customer's stored payment
	// method id when one matching `method` existed at `at`.
	SavedPaymentFor(customerID int64, at time.Time, method string) (int64, bool)
}

// StaffPicker (V1.18.0) answers "which retail spell was on duty at
// this shop at this instant?" — implemented by hr.ShiftIndex; an
// interface here so transactions doesn't import hr (the CustomerPicker
// pattern). Implementations consume RNG only when candidates exist.
type StaffPicker interface {
	PickOnDuty(shopID int64, at time.Time, r *rand.Rand) (int64, bool)
}

// v118 bundles the per-shop V1.18.0 state and streams so the existing
// (already long) emitTransaction signature grows by one parameter.
type v118 struct {
	staff       StaffPicker
	staffRNG    *rand.Rand
	creditRNG   *rand.Rand
	savedPayRNG *rand.Rand
	credit      *creditState
	fraud       *fraudState // V1.24.0 — non-nil only on the fraud shop
}

// Transaction is the generated record — maps to retail.transactions
// with nested transaction_lines and payments for emission.
type Transaction struct {
	TransactionID       int64     `json:"transaction_id"`
	OccurredAt          string    `json:"occurred_at"`
	OccurredAtPrecision string    `json:"occurred_at_precision"`
	ShopID              int64     `json:"shop_id"`
	Channel             string    `json:"channel"`
	CustomerID          *int64    `json:"customer_id"` // V1.14.0: era×channel linkage
	StaffID             *int64    `json:"staff_id"`    // V1.18.0: on-duty spell, rota-covered eras only
	ShippingAddressID   *int64    `json:"shipping_address_id"` // V1.18.0: = customer_id on remote-channel linked sales
	TillID              *string   `json:"till_id"`   // V1.18.0: era-graded, see linkage.go
	DeviceID            *string   `json:"device_id"` // V1.18.0: era-graded, see linkage.go
	CurrencyCode        string    `json:"currency_code"`
	Subtotal            float64   `json:"subtotal"`
	TaxTotal            float64   `json:"tax_total"`
	DiscountTotal       float64   `json:"discount_total"`
	Total               float64   `json:"total"`
	TradeInOffset       float64   `json:"trade_in_offset"`
	OriginalTransactionID *int64  `json:"original_transaction_id,omitempty"` // V1.22: set on refund/return rows
	SourceSystem        string    `json:"source_system"`
	Lines               []Line    `json:"lines"`
	Payments            []Payment `json:"payments"`
	// Inventory + trade-in collections (V1.6.2). Movements has one
	// entry per sale Line plus one per TradeIn item. TradeIn is nil
	// when this transaction has no blended trade-in (~92% of cases).
	Movements          []InventoryMovement `json:"movements,omitempty"`
	NewInventory       []InventoryRow      `json:"new_inventory,omitempty"`
	TradeIn            *TradeIn            `json:"trade_in,omitempty"`
	StoreCreditEvents  []StoreCreditEvent  `json:"store_credit_events,omitempty"`
}

// Line maps to retail.transaction_lines.
type Line struct {
	TransactionLineID int64   `json:"transaction_line_id"`
	LineNumber        int     `json:"line_number"`
	ReleaseID         int64   `json:"release_id"`  // software SKU; 0 on a hardware line
	HardwareID        int64   `json:"hardware_id"` // V1.21: console/hardware model; 0 on a software line
	Condition         string  `json:"condition"`   // 'new' | 'used_mint' | 'used_good' | 'used_fair' | 'used_loose'
	Quantity          int     `json:"quantity"`
	UnitPrice         float64 `json:"unit_price"`
	LineDiscount      float64 `json:"line_discount"`
	LineTax           float64 `json:"line_tax"`
	LineTotal         float64 `json:"line_total"`
	// Inventory bookkeeping (V1.6.2). InventoryID is the deterministic
	// id derived from (shop, release, condition); InventoryNew flags
	// "this is the first time this shop has touched this SKU+condition,
	// so the loader should also emit a fresh inventory row."
	InventoryID  int64 `json:"inventory_id"`
	InventoryNew bool  `json:"inventory_new"`
	OnHand       int   `json:"on_hand"` // snapshot value, only meaningful when InventoryNew
}

// Payment maps to retail.payments.
type Payment struct {
	PaymentID      int64   `json:"payment_id"`
	Method         string  `json:"method"`
	CurrencyCode   string  `json:"currency_code"`
	Amount         float64 `json:"amount"`
	SavedPaymentID *int64  `json:"saved_payment_id"`
	ProcessorRef   *string `json:"processor_ref"`
}

// Sales tax is per-country and era-varying as of V1.22.0 — see
// policy.SalesTaxRate(country, year), applied per line in emitTransaction
// and emitHardwareTransaction. (The old flat const taxRate=0.08 is gone.)

// IDBase supplies the starting ID for each monotonic counter family
// emitTransaction maintains. Callers pass IDBase{} (all zeroes) and
// Generate fills in 1s, so every counter family starts at 1.
type IDBase struct {
	Tx, Line, Payment, Movement   int64
	TradeIn, TradeInItem, Ledger  int64
}

// Generate streams transactions for every shop × day in range.
// Deterministic given (seed, shopList, asOf, catalog TSV). Used for
// single-worker loads and the standalone --emit transactions path.
//
// V1.14.0: cust may be nil (skips customer linkage entirely — every
// transaction emitted with CustomerID=nil) or a customers.Index that
// supplies a customer_id per CustomerLinkageProbability.
func Generate(
	tier string,
	seed uint64,
	asOf time.Time,
	shopList []shops.Shop,
	cat *catalog.Index,
	hw *hardware.Index,
	cust CustomerPicker,
	staff StaffPicker,
	emit func(Transaction),
) (int64, error) {
	return GenerateForShard(tier, seed, asOf, shopList, cat, hw, cust, staff, IDBase{}, emit)
}

// GenerateForShard streams transactions for an arbitrary subset of
// shops, starting each ID family from the supplied IDBase. Generate is
// a thin wrapper over it (the whole shop list, IDBase{}); the web
// capture replay reuses it per shop shard.
//
// Per-shop output is byte-identical for the same (seed, asOf, cat,
// shop) — RNG streams are derived per shop, not from a global counter,
// so which shop subset is passed doesn't reshuffle generation.
func GenerateForShard(
	tier string,
	seed uint64,
	asOf time.Time,
	shopList []shops.Shop,
	cat *catalog.Index,
	hw *hardware.Index,
	cust CustomerPicker,
	staff StaffPicker,
	idBase IDBase,
	emit func(Transaction),
) (int64, error) {
	if cat.Count() == 0 {
		return 0, fmt.Errorf("empty catalog — run build_seed_catalog.py first")
	}

	// Canon-only planted anomalies (the US-0009 fraud pocket). Enabled on
	// the lore-faithful large tiers, stripped on the non-canon small tiers.
	// Computed once here and passed down so the hot per-shop path stays a
	// cheap bool check. Kept in lockstep with hr.emitFraudManager, which
	// gates the culprit's staff spell on the same predicate.
	anomalies := policy.CanonAnomalies(tier)

	pools := computeEligibility(cat, uniqueCountries(shopList))

	// V1.21.0 — hardware demand eligibility (launch-shape × region pools).
	// nil hw → hardware sales disabled (backward compatible).
	var hwElig *hardwareEligibility
	if hw != nil {
		hwElig = computeHardwareEligibility(hw, uniqueCountries(shopList))
	}

	// Default zero IDBase → start every family at 1 (single-shard mode).
	txID := idBase.Tx
	if txID == 0 {
		txID = 1
	}
	lineID := idBase.Line
	if lineID == 0 {
		lineID = 1
	}
	paymentID := idBase.Payment
	if paymentID == 0 {
		paymentID = 1
	}
	movementID := idBase.Movement
	if movementID == 0 {
		movementID = 1
	}
	tradeInID := idBase.TradeIn
	if tradeInID == 0 {
		tradeInID = 1
	}
	tradeInItemID := idBase.TradeInItem
	if tradeInItemID == 0 {
		tradeInItemID = 1
	}
	ledgerID := idBase.Ledger
	if ledgerID == 0 {
		ledgerID = 1
	}

	idCounters := &txCounters{
		tx: &txID, line: &lineID, payment: &paymentID,
		movement: &movementID, tradeIn: &tradeInID,
		tradeInItem: &tradeInItemID, ledger: &ledgerID,
	}

	var totalCount int64
	for _, shop := range shopList {
		n, err := generateShopTransactions(seed, asOf, &shop, cat, hw, pools, hwElig, cust, staff, idCounters, anomalies, emit)
		if err != nil {
			return 0, err
		}
		totalCount += n
	}
	return totalCount, nil
}

// txCounters bundles the eight monotonic ID streams Generate maintains.
// Pulling them into one struct shortens the long shop-loop signature.
type txCounters struct {
	tx, line, payment, movement *int64
	tradeIn, tradeInItem, ledger *int64
}

// eligibilityPools holds the pre-computed sets of catalogue platforms
// that pass IsNewAvailable / IsUsedAvailable for each (country, year),
// AND the regionally-biased cumulative weight of each platform —
// platform-pick is weighted both by catalogue-activity-at-year (via
// PlatformWeightByYear) and by regional sales bias for the shop's
// country (via policy.RegionBiasFor). Built once at sampler startup.
//
// Memory cost: ~18 countries × 45 years × 2 conditions × ~46 platforms
// × ~76 bytes = ~5.7 MB. Trivial.
type eligibilityPools struct {
	newByCountryYear  map[string]map[int]platformPool // country → year → new pool
	usedByCountryYear map[string]map[int]platformPool // country → year → used pool
}

// platformPool is a weighted set of platforms. cumWeight[i] is the
// running sum of weights through platforms[i], so cumWeight[len-1]
// is the total. Caller binary-searches for a uniform target in
// [0, total) to pick a platform proportional to its weight.
type platformPool struct {
	platforms []string
	cumWeight []float64
}

// add appends a platform with weight w, maintaining the prefix-sum
// invariant on cumWeight.
func (p *platformPool) add(name string, w float64) {
	prev := 0.0
	if n := len(p.cumWeight); n > 0 {
		prev = p.cumWeight[n-1]
	}
	p.platforms = append(p.platforms, name)
	p.cumWeight = append(p.cumWeight, prev+w)
}

// sample picks one platform proportional to its weight. Returns
// ("", false) if the pool is empty. Uses binary search over the
// cumulative-weight array — O(log N).
func (p platformPool) sample(r *rand.Rand) (string, bool) {
	n := len(p.platforms)
	if n == 0 {
		return "", false
	}
	total := p.cumWeight[n-1]
	if total <= 0 {
		return p.platforms[r.IntN(n)], true
	}
	target := r.Float64() * total
	idx := sort.SearchFloat64s(p.cumWeight, target)
	if idx >= n {
		idx = n - 1
	}
	return p.platforms[idx], true
}

// computeEligibility walks the catalogue's distinct platforms and the
// 1986-2025 year range for each country represented in the shop list.
// For each (country, year, condition) it builds a weighted platform
// pool where each platform's weight is:
//
//	platform_weight = cat.PlatformWeightByYear(name, year)
//	                × policy.RegionBiasFor(name, country)
//
// Platforms with zero base weight (no releases by year) are excluded;
// platforms with zero regional bias for the country (e.g. ZX Spectrum
// in the US) are also effectively excluded since their weight collapses.
//
// Iteration order over `platforms` is deterministic (sorted) so the
// resulting pools have stable ordering across runs.
func computeEligibility(cat *catalog.Index, countries []string) *eligibilityPools {
	platforms := cat.Platforms()
	sort.Strings(platforms)
	pools := &eligibilityPools{
		newByCountryYear:  make(map[string]map[int]platformPool, len(countries)),
		usedByCountryYear: make(map[string]map[int]platformPool, len(countries)),
	}
	const minYear = 1986
	const maxYear = 2025
	for _, country := range countries {
		newByYear := make(map[int]platformPool, maxYear-minYear+1)
		usedByYear := make(map[int]platformPool, maxYear-minYear+1)
		for year := minYear; year <= maxYear; year++ {
			gen := policy.CurrentGen(year)
			var newPool, usedPool platformPool
			for _, name := range platforms {
				baseWeight := cat.PlatformWeightByYear(name, year)
				if baseWeight <= 0 {
					continue
				}
				biased := baseWeight * policy.RegionBiasFor(name, country)
				if biased <= 0 {
					continue
				}
				lc := policy.PlatformLifecycleFor(name)
				if lc.IsNewAvailable(year) {
					newPool.add(name, biased)
				}
				if lc.IsUsedAvailable(year, gen) {
					usedPool.add(name, biased)
				}
			}
			newByYear[year] = newPool
			usedByYear[year] = usedPool
		}
		pools.newByCountryYear[country] = newByYear
		pools.usedByCountryYear[country] = usedByYear
	}
	return pools
}

// eligible returns the new and used platform pools for the shop's
// country and the transaction year.
func (p *eligibilityPools) eligible(country string, year int) (newPool, usedPool platformPool) {
	if byYear, ok := p.newByCountryYear[country]; ok {
		newPool = byYear[year]
	}
	if byYear, ok := p.usedByCountryYear[country]; ok {
		usedPool = byYear[year]
	}
	return newPool, usedPool
}

// uniqueCountries returns the distinct country codes represented in
// shopList, sorted ascending. Used to scope the per-country
// eligibility precompute to actually-loaded countries (the design has
// 18 countries; smaller tier loads use fewer).
func uniqueCountries(shopList []shops.Shop) []string {
	seen := make(map[string]struct{})
	for _, s := range shopList {
		seen[s.CountryCode] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func generateShopTransactions(
	seed uint64,
	asOf time.Time,
	shop *shops.Shop,
	cat *catalog.Index,
	hw *hardware.Index,
	pools *eligibilityPools,
	hwElig *hardwareEligibility,
	cust CustomerPicker,
	staff StaffPicker,
	ctr *txCounters,
	anomalies bool,
	emit func(Transaction),
) (int64, error) {
	shopOpen, err := time.Parse("2006-01-02", shop.OpenedDate)
	if err != nil {
		return 0, fmt.Errorf("bad opened_date for shop %d: %w", shop.ShopID, err)
	}
	if shopOpen.After(asOf) {
		return 0, nil
	}

	// V1.15.0 — bound the transaction window by (shop.closed_date,
	// BusinessClosureDate, asOf). A shop that shut its doors in 2014
	// doesn't generate transactions in 2015-2016; nothing generates
	// transactions after the 2016-09-30 liquidation.
	dayCutoff := asOf
	if dayCutoff.After(policy.BusinessClosureDate) {
		dayCutoff = policy.BusinessClosureDate
	}
	if shop.ClosedDate != nil {
		if cd, perr := time.Parse("2006-01-02", *shop.ClosedDate); perr == nil && cd.Before(dayCutoff) {
			dayCutoff = cd
		}
	}
	if shopOpen.After(dayCutoff) {
		return 0, nil
	}

	// Shop-level RNGs: one per concern so we can change gen logic
	// without re-shuffling unrelated output.
	ns := fmt.Sprintf("tx/shop/%d", shop.ShopID)
	volRNG := rng.Derive(seed, ns+"/volume")
	channelRNG := rng.Derive(seed, ns+"/channel")
	skuRNG := rng.Derive(seed, ns+"/sku")
	qtyRNG := rng.Derive(seed, ns+"/qty")
	lineCountRNG := rng.Derive(seed, ns+"/line_count")
	payRNG := rng.Derive(seed, ns+"/pay")
	timeOfDayRNG := rng.Derive(seed, ns+"/time_of_day")
	conditionRNG := rng.Derive(seed, ns+"/condition")
	tradeRNG := rng.Derive(seed, ns+"/tradein")
	stockRNG := rng.Derive(seed, ns+"/stock")
	priceRNG := rng.Derive(seed, ns+"/price_jitter") // V1.22: per-line price variance + promos
	returnRNG := rng.Derive(seed, ns+"/returns")     // V1.22: refund roll
	// V1.14.0: per-shop RNG streams for customer-linkage decision and
	// customer-id pick. Two separate streams so changing linkage policy
	// (probability table) doesn't shuffle the customer-pick distribution
	// for transactions that DO get linked.
	custLinkRNG := rng.Derive(seed, ns+"/cust_link")
	custPickRNG := rng.Derive(seed, ns+"/cust_pick")
	inv := newInventoryTracker()

	// V1.18.0: per-shop streams for staff assignment, store-credit
	// redemption decisions, and saved-payment reuse, plus the
	// shop-local credit balance state. Separate streams so each
	// feature's policy can change without reshuffling the others.
	v := &v118{
		staff:       staff,
		staffRNG:    rng.Derive(seed, ns+"/staff_pick"),
		creditRNG:   rng.Derive(seed, ns+"/credit"),
		savedPayRNG: rng.Derive(seed, ns+"/saved_pay"),
		credit:      newCreditState(),
	}
	// V1.24.0 — the fraud pocket arms only on the fraud shop. Its own
	// RNG namespace: every other shop, and every other stream on THIS
	// shop, is byte-identical to a fraud-free build.
	// Canon-only (2026-08): stripped on the non-canon small tiers via the
	// anomalies gate — kept in lockstep with hr.emitFraudManager so a
	// keyed refund never references a staff spell that wasn't emitted.
	if anomalies && shop.ShopCode == policy.FraudShopCode {
		v.fraud = newFraudState(rng.Derive(seed, ns+"/fraud"))
	}

	// Shop's volume_multiplier — log-normal(μ=0, σ=0.5). Drawn once
	// per shop so all its days share the same character (a consistently
	// busy shop stays busy over time).
	volMultRNG := rng.Derive(seed, ns+"/vol_mult")
	volumeMultiplier := sampleLogNormal(volMultRNG, 0.0, 0.5)

	// V1.21.0 — hardware-only RNG streams (additive; the software streams
	// above are untouched, so software content stays byte-identical and the
	// revenue carve-out is a clean volume reduction).
	var hwR hwStreams
	if hw != nil && hwElig != nil {
		hwR = hwStreams{
			vol:       rng.Derive(seed, ns+"/hardware/volume"),
			timeOfDay: rng.Derive(seed, ns+"/hardware/time_of_day"),
			plat:      rng.Derive(seed, ns+"/hardware/platform"),
			model:     rng.Derive(seed, ns+"/hardware/model"),
			cond:      rng.Derive(seed, ns+"/hardware/condition"),
			attach:    rng.Derive(seed, ns+"/hardware/attach"),
			chann:     rng.Derive(seed, ns+"/hardware/channel"),
			pay:       rng.Derive(seed, ns+"/hardware/pay"),
			stock:     rng.Derive(seed, ns+"/hardware/stock"),
			trade:     rng.Derive(seed, ns+"/hardware/tradein"),
			custLink:  rng.Derive(seed, ns+"/hardware/cust_link"),
			custPick:  rng.Derive(seed, ns+"/hardware/cust_pick"),
			accVol:    rng.Derive(seed, ns+"/hardware/acc_vol"),
			accTime:   rng.Derive(seed, ns+"/hardware/acc_time"),
			accAttach: rng.Derive(seed, ns+"/hardware/acc_attach"),
			priceJit:  rng.Derive(seed, ns+"/hardware/price_jitter"),
		}
	}

	basePrice := policy.BasePriceByCurrency[shop.CurrencyCode]
	if basePrice == 0 {
		basePrice = 50.00 // fallback
	}
	minorUnit := policy.MinorUnit(shop.CurrencyCode)

	var shopCount int64
	for day := shopOpen; !day.After(dayCutoff); day = day.AddDate(0, 0, 1) {
		expected := policy.DailyTxPerShopMedian(day.Year()) *
			volumeMultiplier *
			policy.SeasonalMultiplier(day.Month()) *
			policy.TransactionVolumeFor(day.Year()) // V1.15.0: decline taper
		n := samplePoisson(volRNG, expected)
		// V1.18.1: draw all of today's time-of-day values up front and
		// SORT them, so within-shop generation order == clock order.
		// Pre-V1.18.1 the k-th transaction of a day could carry an
		// earlier time than the (k-1)-th, which let a store-credit
		// redemption be timestamped before the same-day grant that
		// funded it (and saved cards before their added_at). Same
		// number of draws from the same stream — only the pairing of
		// times to transactions changes. Bonus realism: transaction
		// ids now ascend with time within a day, like a real POS.
		secs := make([]int, n)
		for i := range secs {
			secs[i] = timeOfDayRNG.IntN(86400)
		}
		sort.Ints(secs)
		for i := 0; i < n; i++ {
			emitTransaction(
				ctr,
				shop, shopOpen, day, secs[i], basePrice, minorUnit,
				cat, pools, shop.CountryCode, inv,
				channelRNG, skuRNG, qtyRNG, lineCountRNG, payRNG,
				conditionRNG, tradeRNG, stockRNG, priceRNG, returnRNG,
				cust, custLinkRNG, custPickRNG,
				v,
				emit,
			)
			shopCount++
		}

		// V1.21.0 — hardware demand pass for this shop-day (separate
		// streams; runs after the software emit for the day).
		if hw != nil && hwElig != nil {
			hwExpected := policy.HardwareUnitsPerShopDay(day.Year()) *
				volumeMultiplier *
				policy.SeasonalMultiplier(day.Month()) *
				policy.TransactionVolumeFor(day.Year()) // V1.21.1: hardware collapses with the company in the decline
			hwN := samplePoisson(hwR.vol, hwExpected)
			hwSecs := make([]int, hwN)
			for i := range hwSecs {
				hwSecs[i] = hwR.timeOfDay.IntN(86400)
			}
			sort.Ints(hwSecs)
			for i := 0; i < hwN; i++ {
				emitHardwareTransaction(ctr, shop, shopOpen, day, hwSecs[i], minorUnit,
					hw, hwElig, cat, shop.CountryCode, inv, hwR, cust, v, false, emit)
				shopCount++
			}

			// V1.21.2 — standalone accessory purchases (replacement
			// controllers, memory cards for consoles already owned).
			accExpected := policy.AccessoryUnitsPerShopDay(day.Year()) *
				volumeMultiplier *
				policy.SeasonalMultiplier(day.Month()) *
				policy.TransactionVolumeFor(day.Year())
			accN := samplePoisson(hwR.accVol, accExpected)
			accSecs := make([]int, accN)
			for i := range accSecs {
				accSecs[i] = hwR.accTime.IntN(86400)
			}
			sort.Ints(accSecs)
			for i := 0; i < accN; i++ {
				emitHardwareTransaction(ctr, shop, shopOpen, day, accSecs[i], minorUnit,
					hw, hwElig, cat, shop.CountryCode, inv, hwR, cust, v, true, emit)
				shopCount++
			}
		}

		// V1.24.0 — the fraud pocket: after the day's honest trade, the
		// manager stays late. No-ops everywhere but the fraud shop
		// inside the fraud window.
		if v.fraud != nil {
			v.fraud.emitFraudDay(ctr, shop, day, minorUnit, emit)
		}
	}
	return shopCount, nil
}

func emitTransaction(
	ctr *txCounters,
	shop *shops.Shop,
	shopOpen time.Time,
	day time.Time,
	secondOfDay int, // V1.18.1: drawn + sorted by the caller's day loop
	basePrice float64,
	minorUnit int,
	cat *catalog.Index,
	pools *eligibilityPools,
	country string,
	inv *inventoryTracker,
	channelRNG, skuRNG, qtyRNG, lineCountRNG, payRNG *rand.Rand,
	conditionRNG, tradeRNG, stockRNG, priceRNG, returnRNG *rand.Rand,
	cust CustomerPicker,
	custLinkRNG, custPickRNG *rand.Rand,
	v *v118,
	emit func(Transaction),
) {
	// Timestamp within the day — second-of-day pre-drawn and sorted by
	// the day loop (V1.18.1); precision then applied per §9.10.2.
	occurredAt := day.Add(time.Duration(secondOfDay) * time.Second)
	precision := policy.SignupPrecisionForYear(occurredAt.Year()) // reuse §9.10.2 ladder
	occurredAt = coarsenToPrecision(occurredAt, precision)
	// V1.10: clamp to shop opening date. Coarsening to year/month
	// precision can shift the timestamp BEFORE the shop opened
	// (e.g. 1986-08-15 with year-precision → 1986-01-01, but Shop 1
	// opened 1986-08-06). Affects only the opening year; subsequent
	// years coarsen to Jan 1 which is already >= shopOpen.
	if occurredAt.Before(shopOpen) {
		occurredAt = shopOpen
	}
	sourceSys := transactionSourceSystem(occurredAt.Year())

	channel := pickChannel(channelRNG, occurredAt.Year())

	lineCount := sampleLineCount(lineCountRNG, occurredAt.Year())
	lines := make([]Line, 0, lineCount)
	movements := make([]InventoryMovement, 0, lineCount)
	var newInv []InventoryRow
	var subtotal float64
	var taxTotal float64
	var discountTotal float64
	// V1.17.0: liquidation markdowns land on the receipt. Rate is 0
	// outside Apr-Sep 2016, so the math below is byte-identical to
	// pre-V1.17 for 99.5% of transactions (audit finding F2b).
	fireSaleRate := policy.FireSaleDiscountRate(occurredAt)
	txTaxRate := policy.SalesTaxRate(country, occurredAt.Year()) // V1.22: per-country/era
	for i := 0; i < lineCount; i++ {
		rel, condition, ok := pickConditionAndRelease(occurredAt, country, pools, cat, conditionRNG, skuRNG)
		if !ok {
			continue
		}
		// V1.22: per-line price jitter (±) + occasional promo markdown so a
		// SKU isn't sold at one identical price forever (audit flatness tell).
		unitPrice := priceFor(rel, basePrice, occurredAt.Year(), minorUnit, condition)
		unitPrice = jitterPrice(unitPrice, minorUnit, priceRNG)
		qty := sampleQuantity(qtyRNG)
		gross := unitPrice * float64(qty)
		lineDiscount := 0.0
		if fireSaleRate > 0 {
			lineDiscount = roundTo(gross*fireSaleRate, minorUnit)
		} else if promo := promoMarkdown(occurredAt.Year(), priceRNG); promo > 0 {
			lineDiscount = roundTo(gross*promo, minorUnit)
		}
		taxable := gross - lineDiscount
		lineTax := roundTo(taxable*txTaxRate, minorUnit)
		lineTotal := roundTo(taxable+lineTax, minorUnit)
		invID := InventoryIDFor(shop.ShopID, rel.ReleaseID, condition)
		isNew := inv.touch(rel.ReleaseID, condition)
		line := Line{
			TransactionLineID: *ctr.line,
			LineNumber:        i + 1,
			ReleaseID:         rel.ReleaseID,
			Condition:         condition,
			Quantity:          qty,
			UnitPrice:         unitPrice,
			LineDiscount:      lineDiscount,
			LineTax:           lineTax,
			LineTotal:         lineTotal,
			InventoryID:       invID,
			InventoryNew:      isNew,
		}
		if isNew {
			line.OnHand = onHandSampleFor(condition, stockRNG)
			newInv = append(newInv, InventoryRow{
				InventoryID: invID,
				ShopID:      shop.ShopID,
				ReleaseID:   rel.ReleaseID,
				Condition:   condition,
				OnHand:      line.OnHand,
			})
		}
		lines = append(lines, line)
		txIDValue := *ctr.tx
		movements = append(movements, InventoryMovement{
			MovementID:          *ctr.movement,
			InventoryID:         invID,
			OccurredAt:          formatTimestamp(occurredAt, precision),
			OccurredAtPrecision: precision,
			MovementType:        "sale",
			Quantity:            -qty,
			ReferenceType:       stringPtr("transaction"),
			ReferenceID:         &txIDValue,
			SourceSystem:        sourceSys,
		})
		*ctr.movement++
		*ctr.line++
		subtotal += gross
		taxTotal += lineTax
		discountTotal += lineDiscount
	}
	subtotal = roundTo(subtotal, minorUnit)
	taxTotal = roundTo(taxTotal, minorUnit)
	discountTotal = roundTo(discountTotal, minorUnit)
	// The receipt identity the schema documents (and V1.17.0 finally
	// makes true): total = subtotal - discount_total + tax_total.
	// trade_in_offset is NOT part of total — it reduces what gets PAID
	// (see the payments adjustment below).
	total := roundTo(subtotal-discountTotal+taxTotal, minorUnit)

	if len(lines) == 0 {
		return // no catalog available yet (e.g., 1986-1977 gap)
	}

	payment := Payment{
		PaymentID:    *ctr.payment,
		Method:       pickPaymentMethod(payRNG, occurredAt.Year(), channel),
		CurrencyCode: shop.CurrencyCode,
		Amount:       total,
	}
	*ctr.payment++

	// V1.14.0: customer linkage. Roll the era×channel-aware probability;
	// if positive AND the country has any eligible customer at this date,
	// attach a customer_id. Pre-online in-store sales are 95% anonymous
	// by design; modern online/mobile/click_and_collect always linked.
	var customerID *int64
	if cust != nil && custLinkRNG.Float64() < policy.CustomerLinkageProbability(channel, occurredAt.Year()) {
		if cid, ok := cust.Pick(shop.CountryCode, occurredAt, custPickRNG); ok {
			customerID = &cid
		}
	}

	tx := Transaction{
		TransactionID:       *ctr.tx,
		OccurredAt:          formatTimestamp(occurredAt, precision),
		OccurredAtPrecision: precision,
		ShopID:              shop.ShopID,
		Channel:             channel,
		CustomerID:          customerID,
		CurrencyCode:        shop.CurrencyCode,
		Subtotal:            subtotal,
		TaxTotal:            taxTotal,
		DiscountTotal:       discountTotal,
		Total:               total,
		SourceSystem:        sourceSys,
		Lines:               lines,
		Payments:            []Payment{payment},
		Movements:           movements,
		NewInventory:        newInv,
	}

	// Trade-in roll: blended trade-in attached to this transaction.
	// Era-aware probability per §9.14.2; probability rises through the
	// 90s as the used-game market emerges.
	grantedThisTx := false
	if tradeRNG.Float64() < tradeInProbability(occurredAt.Year()) {
		ti := buildTradeIn(ctr, shop, occurredAt, precision, sourceSys, basePrice,
			minorUnit, country, pools, cat, tradeRNG, conditionRNG, skuRNG, stockRNG,
			customerID)
		if ti != nil {
			ti.TransactionID = &tx.TransactionID
			tx.TradeIn = ti
			if ti.PayoutMethod == "store_credit" && customerID != nil {
				// V1.18.0: store-credit payouts flow through the LEDGER,
				// not the offset — setting both would pay the customer
				// twice. The grant lands on the shop-local balance and
				// the redemption block below typically spends it on this
				// very receipt.
				tx.TradeInOffset = 0
				v.credit.grant(*customerID, ti.TotalValue)
				grantedThisTx = true
				tiID := ti.TradeInID
				tx.StoreCreditEvents = append(tx.StoreCreditEvents, StoreCreditEvent{
					LedgerID:      *ctr.ledger,
					CustomerID:    *customerID,
					OccurredAt:    formatTimestamp(occurredAt, precision),
					EventType:     "credit_granted",
					Amount:        ti.TotalValue,
					CurrencyCode:  shop.CurrencyCode,
					TradeInID:     &tiID,
					TransactionID: &tx.TransactionID,
					SourceSystem:  sourceSys,
				})
				*ctr.ledger++
			} else {
				// V1.17.0: cash/bank payouts settle part of the receipt
				// via the offset (audit finding F2). Offset caps at the
				// receipt total; payments cover the remainder, and a
				// receipt fully settled by trade-in credit has no
				// payment row at all.
				offset := ti.TotalValue
				if offset > total {
					offset = total
				}
				tx.TradeInOffset = offset
				remaining := roundTo(total-offset, minorUnit)
				if remaining <= 0 {
					tx.TradeInOffset = total
					tx.Payments = nil
				} else {
					tx.Payments[0].Amount = remaining
				}
			}
			// Trade-in items create positive inventory movements
			// (used stock arrives at the shop).
			for _, item := range ti.Items {
				invID := InventoryIDFor(shop.ShopID, item.ReleaseID, item.Condition)
				if inv.touch(item.ReleaseID, item.Condition) {
					tx.NewInventory = append(tx.NewInventory, InventoryRow{
						InventoryID: invID,
						ShopID:      shop.ShopID,
						ReleaseID:   item.ReleaseID,
						Condition:   item.Condition,
						OnHand:      onHandSampleFor(item.Condition, stockRNG),
					})
				}
				tiID := ti.TradeInID
				tx.Movements = append(tx.Movements, InventoryMovement{
					MovementID:          *ctr.movement,
					InventoryID:         invID,
					OccurredAt:          formatTimestamp(occurredAt, precision),
					OccurredAtPrecision: precision,
					MovementType:        "trade_in",
					Quantity:            +1,
					ReferenceType:       stringPtr("trade_in"),
					ReferenceID:         &tiID,
					SourceSystem:        sourceSys,
				})
				*ctr.movement++
			}
		}
	}

	// ---- V1.18.0 linkage features ----

	// Staff on duty: in-store-style channels pick a rostered retail
	// spell; the blended trade-in is handled by the same associate.
	// NULL outside rota coverage (each shop's final shiftHistoryYears).
	if v.staff != nil && (channel == "in_store" || channel == "click_and_collect") {
		if spellID, ok := v.staff.PickOnDuty(shop.ShopID, occurredAt, v.staffRNG); ok {
			tx.StaffID = &spellID
			if tx.TradeIn != nil {
				tx.TradeIn.StaffID = &spellID
			}
		}
	}

	// Shipping address: remote-channel orders for identified customers
	// ship to the customer's address. V1 has exactly one current
	// address per customer with customer_address_id = customer_id
	// (verified identity), so no lookup structure is needed.
	if customerID != nil && remoteChannels[channel] {
		addrID := *customerID
		tx.ShippingAddressID = &addrID
	}

	// Store-credit redemption: spend shop-local balance on this
	// receipt. A grant made on THIS transaction is always applied
	// (customers trade in to fund the purchase in hand); balance
	// carried from earlier visits redeems with p=0.5. The redeemed
	// portion becomes a second payment row so the V1.17 identity
	// SUM(payments) = total - trade_in_offset still holds exactly.
	if customerID != nil && len(tx.Payments) > 0 && v.credit.available(*customerID) > 0 {
		apply := grantedThisTx
		if !apply {
			apply = v.creditRNG.Float64() < 0.5
		}
		if apply {
			if redeemed := v.credit.redeem(*customerID, tx.Payments[0].Amount, minorUnit); redeemed > 0 {
				tx.StoreCreditEvents = append(tx.StoreCreditEvents, StoreCreditEvent{
					LedgerID:      *ctr.ledger,
					CustomerID:    *customerID,
					OccurredAt:    formatTimestamp(occurredAt, precision),
					EventType:     "credit_used",
					Amount:        -redeemed,
					CurrencyCode:  shop.CurrencyCode,
					TransactionID: &tx.TransactionID,
					SourceSystem:  sourceSys,
				})
				*ctr.ledger++
				remaining := roundTo(tx.Payments[0].Amount-redeemed, minorUnit)
				creditPay := Payment{
					PaymentID:    *ctr.payment,
					Method:       "store_credit",
					CurrencyCode: shop.CurrencyCode,
					Amount:       redeemed,
				}
				*ctr.payment++
				if remaining <= 0 {
					tx.Payments = []Payment{creditPay}
				} else {
					tx.Payments[0].Amount = remaining
					tx.Payments = append(tx.Payments, creditPay)
				}
			}
		}
	}

	// Saved payment method: identified customers on remote channels
	// reuse their stored method (p=0.75) when the sampled method
	// matches one they had on file at the time. RNG drawn only on a
	// successful lookup, keeping the stream self-contained.
	if customerID != nil && occurredAt.Year() >= 2008 && remoteChannels[channel] &&
		len(tx.Payments) > 0 && savedPayMethods[tx.Payments[0].Method] {
		if spID, ok := cust.SavedPaymentFor(*customerID, occurredAt, tx.Payments[0].Method); ok {
			if v.savedPayRNG.Float64() < 0.75 {
				tx.Payments[0].SavedPaymentID = &spID
			}
		}
	}

	// Era-graded till / device / processor identifiers — pure
	// functions of already-assigned ids (see linkage.go).
	year := occurredAt.Year()
	if channel == "in_store" {
		tx.TillID = tillIDFor(tx.TransactionID, shop.ShopID, year)
	}
	tx.DeviceID = deviceIDFor(tx.TransactionID, channel, year)
	for i := range tx.Payments {
		tx.Payments[i].ProcessorRef = processorRefFor(tx.Payments[i].PaymentID, tx.Payments[i].Method, year)
	}

	*ctr.tx++
	emit(tx)

	// V1.22 — product return: a fraction of sales are refunded a few days
	// later. The refund is a separate negative-total transaction linked via
	// original_transaction_id; it returns the original tender (cash/card —
	// NOT store credit, so the ledger invariant is untouched) and puts the
	// stock back. Only sales paid by a normal tender are eligible (a sale
	// fully settled by store credit / trade-in has no refundable payment).
	if returnRNG.Float64() < policy.ReturnProbability(occurredAt.Year()) {
		emitReturn(ctr, &tx, occurredAt, precision, sourceSys, minorUnit, returnRNG, emit)
	} else if v.fraud != nil {
		// V1.24.0 — an organically-unrefunded sale is a candidate target
		// for the fraud shop's keyed refunds (a parent is never refunded
		// twice: the organic roll above already passed on this one).
		v.fraud.maybeBuffer(&tx, occurredAt)
	}
}

// emitReturn emits a refund transaction for one line of a parent sale.
// V1.22.0. Negative subtotal/tax/total, a negative payment of the same
// method, and a positive ('return') inventory movement that restocks the
// item. Dated 1-21 days after the sale, clamped to the liquidation date.
func emitReturn(
	ctr *txCounters, parent *Transaction,
	saleAt time.Time, precision, sourceSys string, minorUnit int,
	r *rand.Rand, emit func(Transaction),
) {
	if len(parent.Lines) == 0 || len(parent.Payments) == 0 {
		return
	}
	// Refund original tender only — never store credit (keeps the ledger
	// invariant intact and matches real refund-to-original-payment policy).
	pay := parent.Payments[0]
	if pay.Method == "store_credit" || pay.Amount <= 0 {
		return
	}
	// Return one line (real returns are usually a single item).
	ln := parent.Lines[r.IntN(len(parent.Lines))]
	if ln.LineTotal <= 0 {
		return
	}
	lagDays := 1 + r.IntN(21)
	retAt := saleAt.AddDate(0, 0, lagDays)
	if retAt.After(policy.BusinessClosureDate) {
		retAt = policy.BusinessClosureDate
	}
	if !retAt.After(saleAt) {
		return
	}
	retPrecision := policy.SignupPrecisionForYear(retAt.Year())
	retAt = coarsenToPrecision(retAt, retPrecision)
	if !retAt.After(saleAt) {
		retAt = saleAt.AddDate(0, 0, 1)
	}

	refundTotal := roundTo(ln.LineTotal, minorUnit)
	refundTax := roundTo(ln.LineTax, minorUnit)
	refundSub := roundTo(ln.UnitPrice*float64(ln.Quantity)-ln.LineDiscount, minorUnit)
	parentID := parent.TransactionID

	rtx := Transaction{
		TransactionID:         *ctr.tx,
		OccurredAt:            formatTimestamp(retAt, retPrecision),
		OccurredAtPrecision:   retPrecision,
		ShopID:                parent.ShopID,
		Channel:               "in_store", // returns are handled at the counter
		CustomerID:            parent.CustomerID,
		CurrencyCode:          parent.CurrencyCode,
		Subtotal:              -refundSub,
		TaxTotal:              -refundTax,
		DiscountTotal:         0,
		Total:                 -refundTotal,
		SourceSystem:          sourceSys,
		OriginalTransactionID: &parentID,
		Lines: []Line{{
			TransactionLineID: *ctr.line,
			LineNumber:        1,
			ReleaseID:         ln.ReleaseID,
			HardwareID:        ln.HardwareID,
			Condition:         ln.Condition,
			Quantity:          -ln.Quantity,
			UnitPrice:         ln.UnitPrice,
			LineDiscount:      0,
			LineTax:           -refundTax,
			LineTotal:         -refundTotal,
			InventoryID:       ln.InventoryID,
		}},
		Payments: []Payment{{
			PaymentID:    *ctr.payment,
			Method:       pay.Method,
			CurrencyCode: parent.CurrencyCode,
			Amount:       -refundTotal,
		}},
		Movements: []InventoryMovement{{
			MovementID:          *ctr.movement,
			InventoryID:         ln.InventoryID,
			OccurredAt:          formatTimestamp(retAt, retPrecision),
			OccurredAtPrecision: retPrecision,
			MovementType:        "return",
			Quantity:            ln.Quantity, // stock comes back (positive)
			ReferenceType:       stringPtr("transaction"),
			ReferenceID:         &parentID,
			SourceSystem:        sourceSys,
		}},
	}
	rtx.Payments[0].ProcessorRef = processorRefFor(rtx.Payments[0].PaymentID, pay.Method, retAt.Year())
	if rtx.Channel == "in_store" {
		rtx.TillID = tillIDFor(rtx.TransactionID, parent.ShopID, retAt.Year())
	}
	*ctr.line++
	*ctr.payment++
	*ctr.movement++
	*ctr.tx++
	emit(rtx)
}

// buildTradeIn constructs a TradeIn (1-3 items) and rolls per §9.14.
// Returns nil if no items survived the catalog-availability check.
func buildTradeIn(
	ctr *txCounters,
	shop *shops.Shop, occurredAt time.Time, precision, sourceSys string,
	basePrice float64, minorUnit int, country string,
	pools *eligibilityPools, cat *catalog.Index,
	tradeRNG, conditionRNG, skuRNG, stockRNG *rand.Rand,
	customerID *int64, // V1.18.0: the blended sale's customer (nil = walk-in)
) *TradeIn {
	year := occurredAt.Year()
	// 1-3 items skewed toward 1.
	itemCount := 1
	switch u := tradeRNG.Float64(); {
	case u < 0.65:
		itemCount = 1
	case u < 0.92:
		itemCount = 2
	default:
		itemCount = 3
	}
	items := make([]TradeInItem, 0, itemCount)
	var total float64
	// Trade-ins draw from the used-eligibility pool — people trade in
	// games they're done with, which by definition are older releases.
	_, usedPool := pools.eligible(country, year)
	if len(usedPool.platforms) == 0 {
		return nil
	}
	eraFactor := policy.EraPriceFactor(year)
	for i := 0; i < itemCount; i++ {
		platform, ok := usedPool.sample(skuRNG)
		if !ok {
			continue
		}
		rel, ok := cat.SampleForPlatformWeighted(platform, occurredAt, skuRNG)
		if !ok {
			continue
		}
		condition := sampleTradeInCondition(conditionRNG)
		ageDisc := policy.AgeDiscount(year - rel.ReleaseDate.Year())
		val := tradeInValue(basePrice, eraFactor, ageDisc, condition, minorUnit)
		items = append(items, TradeInItem{
			TradeInItemID: *ctr.tradeInItem,
			ReleaseID:     rel.ReleaseID,
			Condition:     condition,
			Valuation:     val,
		})
		*ctr.tradeInItem++
		total += val
	}
	if len(items) == 0 {
		return nil
	}
	ti := &TradeIn{
		TradeInID:           *ctr.tradeIn,
		OccurredAt:          formatTimestamp(occurredAt, precision),
		OccurredAtPrecision: precision,
		ShopID:              shop.ShopID,
		CustomerID:          customerID, // V1.18.0: ~40% identified via the blended sale
		CurrencyCode:        shop.CurrencyCode,
		TotalValue:          roundTo(total, minorUnit),
		PayoutMethod:        samplePayoutMethod(tradeRNG, customerID != nil),
		SourceSystem:        sourceSys,
		Items:               items,
	}
	*ctr.tradeIn++
	return ti
}

func stringPtr(s string) *string { return &s }

// ---------- sampling helpers ----------

// samplePoisson returns a Poisson-distributed integer with mean λ.
// Knuth's algorithm — good enough for λ up to a few hundred, which is
// the per-shop-day regime.
func samplePoisson(r *rand.Rand, lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	L := math.Exp(-lambda)
	k := 0
	p := 1.0
	for {
		k++
		p *= r.Float64()
		if p <= L {
			return k - 1
		}
	}
}

// sampleLogNormal returns exp(μ + σZ) with Z ~ N(0,1) via Box–Muller.
func sampleLogNormal(r *rand.Rand, mu, sigma float64) float64 {
	u1, u2 := r.Float64(), r.Float64()
	if u1 < 1e-9 {
		u1 = 1e-9
	}
	z := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	return math.Exp(mu + sigma*z)
}

// sampleLineCount gives 1–3 lines, skewed toward 1.
// V1.20: cutoffs drift by era so items-per-basket isn't a robotic 1.50
// every single year (audit finding #6 — "zero variance"). Boom-era
// shoppers (2001-2010, peak disposable spending + the holiday-blockbuster
// years) carry slightly fuller baskets than the lean early and decline
// eras. Mean basket: ~1.46 (≤2000) → ~1.55 (2001-2010) → ~1.50 (2011+).
// Still one Float64 draw — same RNG cadence, only the thresholds move.
func sampleLineCount(r *rand.Rand, year int) int {
	var p1, p2 float64
	switch {
	case year <= 2000:
		p1, p2 = 0.63, 0.91 // mean ≈ 1.46
	case year <= 2010:
		p1, p2 = 0.57, 0.88 // mean ≈ 1.55
	default:
		p1, p2 = 0.60, 0.90 // mean ≈ 1.50
	}
	u := r.Float64()
	switch {
	case u < p1:
		return 1
	case u < p2:
		return 2
	default:
		return 3
	}
}

// sampleQuantity gives 1–2, overwhelmingly 1. Realistic for retail
// gaming — customers rarely buy multiple copies of the same title.
func sampleQuantity(r *rand.Rand) int {
	if r.Float64() < 0.97 {
		return 1
	}
	return 2
}

// pickChannel samples a channel per §9.15.6 weights for the year.
func pickChannel(r *rand.Rand, year int) string {
	w := policy.ChannelMixForYear(year)
	u := r.Float64()
	cum := 0.0
	for _, pair := range []struct {
		name   string
		weight float64
	}{
		{"in_store", w.InStore},
		{"phone", w.Phone},
		{"online", w.Online},
		{"click_and_collect", w.ClickAndCollect},
		{"mobile_app", w.MobileApp},
	} {
		cum += pair.weight
		if u <= cum {
			return pair.name
		}
	}
	return "in_store"
}

// pickPaymentMethod samples per §9.10.4 weights.
func pickPaymentMethod(r *rand.Rand, year int, channel string) string {
	weights := policy.PaymentMethodsForYear(year, channel)
	u := r.Float64()
	cum := 0.0
	for _, w := range weights {
		cum += w.Weight
		if u <= cum {
			return w.Method
		}
	}
	if len(weights) > 0 {
		return weights[len(weights)-1].Method
	}
	return "cash"
}

// priceFor computes the unit price of a line. Base × era × age ×
// condition multipliers, rounded to currency minor-unit. Condition is
// "new" → 1.00× through "used_loose" → 0.25× (see policy.ConditionPriceMultiplier).
func priceFor(rel catalog.Release, basePrice float64, txYear int, minorUnit int, condition string) float64 {
	age := txYear - rel.ReleaseDate.Year()
	raw := basePrice *
		policy.EraPriceFactor(txYear) *
		policy.AgeDiscount(age) *
		policy.ConditionPriceMultiplier(condition)
	return roundTo(raw, minorUnit)
}

// jitterPrice applies a small symmetric variation to a catalogue price so
// the same SKU isn't sold at one identical price forever — store-to-store
// and over-time pricing spread, promotional pre-markdown drift, regional
// MSRP differences. V1.22.0. Symmetric (mean 1.0) so it's revenue-neutral
// in expectation; bounded to ±priceJitterPct. One Float64 draw.
const priceJitterPct = 0.09

func jitterPrice(price float64, minorUnit int, r *rand.Rand) float64 {
	if price <= 0 {
		return price
	}
	factor := 1.0 + (r.Float64()*2.0-1.0)*priceJitterPct
	return roundTo(price*factor, minorUnit)
}

// promoMarkdown returns an occasional promotional discount fraction (sale
// events, bundle/clearance pricing) outside the 2016 fire sale. ~8% of
// lines get a 10-30% markdown — enough to make a SKU's price distribution
// realistic without moving aggregate revenue much. One Float64 draw (only
// the trigger; the size is derived from the same draw to keep it cheap).
func promoMarkdown(year int, r *rand.Rand) float64 {
	if r.Float64() >= policy.PromoLineFraction(year) {
		return 0
	}
	// 10-30% off, centred ~20%.
	return 0.10 + 0.20*r.Float64()
}

// pickConditionAndRelease decides whether a line is for a new or used
// copy, picks an eligible platform from the appropriate pool, then
// samples a release from that platform whose ReleaseDate ≤ occurredAt.
//
// Returns (rel, condition, true) on success. Returns ok=false only
// when both pools are empty (e.g. catalogue mismatch — should not
// happen in practice). When the year's preferred pool is empty
// (e.g. 2024+ has no "new" platforms after PS4 ages out), falls back
// to the other pool — never blocks a sale solely on lifecycle.
//
// Two RNGs:
//   - conditionRNG: new-vs-used decision and used-grade roll
//   - skuRNG: platform pick within a pool, and release-within-platform pick
//
// They're separate so adding lifecycle logic doesn't reshuffle existing
// SKU sampling order beyond what's intended.
func pickConditionAndRelease(
	occurredAt time.Time,
	country string,
	pools *eligibilityPools,
	cat *catalog.Index,
	conditionRNG, skuRNG *rand.Rand,
) (catalog.Release, string, bool) {
	year := occurredAt.Year()
	newPool, usedPool := pools.eligible(country, year)
	canNew := len(newPool.platforms) > 0
	canUsed := len(usedPool.platforms) > 0
	if !canNew && !canUsed {
		// Catalogue empty for this year — fall back to whatever the
		// global SampleAvailable can find. Treat as new for pricing.
		rel, ok := cat.SampleAvailable(occurredAt, skuRNG)
		if !ok {
			return catalog.Release{}, "", false
		}
		return rel, policy.ConditionNew, true
	}

	// Decide preferred condition; fall back to the other pool if the
	// preferred one is empty for this year.
	wantNew := canNew && (!canUsed || conditionRNG.Float64() < policy.NewVsUsedSplit(year))
	if wantNew {
		if platform, ok := newPool.sample(skuRNG); ok {
			if rel, ok := cat.SampleForPlatformWeighted(platform, occurredAt, skuRNG); ok {
				return rel, policy.ConditionNew, true
			}
		}
		// Pool weights guarantee the chosen platform has releases by
		// occurredAt, but the inner sample can still fail at edges —
		// fall through to used path.
	}
	if canUsed {
		if platform, ok := usedPool.sample(skuRNG); ok {
			if rel, ok := cat.SampleForPlatformWeighted(platform, occurredAt, skuRNG); ok {
				return rel, sampleUsedCondition(conditionRNG), true
			}
		}
	}
	// Final fallback: any release from any platform.
	rel, ok := cat.SampleAvailable(occurredAt, skuRNG)
	if !ok {
		return catalog.Release{}, "", false
	}
	return rel, policy.ConditionNew, true
}

// sampleUsedCondition picks a used grade per policy.UsedConditionWeights
// (mint 25% / good 45% / fair 22% / loose 8%, §9.14.5 inflow distribution).
func sampleUsedCondition(r *rand.Rand) string {
	u := r.Float64()
	for _, w := range policy.UsedConditionWeights {
		if u < w.Cum {
			return w.Condition
		}
	}
	return policy.ConditionUsedLoose
}

// roundTo rounds to `places` decimal places.
func roundTo(v float64, places int) float64 {
	mult := math.Pow10(places)
	return math.Round(v*mult) / mult
}

// coarsenToPrecision zero-fills below the given precision level.
// Duplicated from customers package to keep transactions standalone.
func coarsenToPrecision(t time.Time, precision string) time.Time {
	switch precision {
	case "year":
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case "hour":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
	case "minute":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	case "second":
		return t.UTC().Truncate(time.Second)
	default: // millisecond
		return t.UTC().Truncate(time.Millisecond)
	}
}

func formatTimestamp(t time.Time, precision string) string {
	if precision == "millisecond" {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// transactionSourceSystem tags a tx with the POS lineage per §9.10.1.
func transactionSourceSystem(year int) string {
	switch {
	case year <= 2003:
		return "pos_legacy_1986_2003"
	case year <= 2015:
		return "pos_transitional_2004_2015"
	default:
		return "pos_current"
	}
}
