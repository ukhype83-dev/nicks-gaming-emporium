// V1.21.0 — console / hardware sales.
//
// Hardware sells OPPOSITE to games: a console spikes at launch then
// declines, is sold near cost (razor-and-blade), drops in price down its
// revision chain, and pulls 1-3 same-platform games into the basket (the
// attach rate). A console purchase may also be blended with a used-console
// trade-in (the upgrade cycle). This runs as a SEPARATE per-shop-day demand
// pass on brand-new RNG streams (tx/shop/<id>/hardware/*), so the existing
// software streams' per-transaction content is unchanged — only their COUNT
// drops via policy.softwareVolumeCarveout, which keeps the revenue-neutral
// carve-out calibration clean.
package transactions

import (
	"math/rand/v2"
	"sort"
	"time"

	"emporium/internal/catalog"
	"emporium/internal/hardware"
	"emporium/internal/policy"
	"emporium/internal/shops"
)

// hardwareEligibility holds, per (country, year), a weighted pool of the
// platforms whose hardware is sellable — weighted by launch-shape demand
// (the launch spike) × regional bias. Built once at sampler startup,
// mirroring computeEligibility. Reuses platformPool.
type hardwareEligibility struct {
	byCountryYear map[string]map[int]platformPool
}

// hardwareDiscontinueTailYears — how long after a platform's discontinue
// year it still sells hardware (new clearance + a short used tail).
const hardwareDiscontinueTailYears = 3

func computeHardwareEligibility(hw *hardware.Index, countries []string) *hardwareEligibility {
	platforms := hw.Platforms()
	sort.Strings(platforms)
	launchYear := make(map[string]int, len(platforms))
	for _, p := range platforms {
		if l := hw.PlatformLaunch(p); !l.IsZero() {
			launchYear[p] = l.Year()
		}
	}
	he := &hardwareEligibility{byCountryYear: make(map[string]map[int]platformPool, len(countries))}
	const minYear, maxYear = 1986, 2025
	for _, country := range countries {
		byYear := make(map[int]platformPool, maxYear-minYear+1)
		for year := minYear; year <= maxYear; year++ {
			var pool platformPool
			for _, name := range platforms {
				ly, ok := launchYear[name]
				if !ok {
					continue
				}
				// Lifecycle gate: a platform stops selling hardware a few
				// years after it's discontinued (new clearance + a short
				// used-console tail). Without this the launch-shape floor
				// keeps e.g. the NES selling into 2010 as total hardware
				// volume grows — a realism tell.
				if lc := policy.PlatformLifecycleFor(name); lc.DiscontinuedYear > 0 &&
					year > lc.DiscontinuedYear+hardwareDiscontinueTailYears {
					continue
				}
				shape := policy.HardwareLaunchShape(year - ly)
				if shape <= 0 {
					continue
				}
				w := shape * policy.RegionBiasFor(name, country)
				if w <= 0 {
					continue
				}
				pool.add(name, w)
			}
			byYear[year] = pool
		}
		he.byCountryYear[country] = byYear
	}
	return he
}

func (h *hardwareEligibility) pool(country string, year int) platformPool {
	if byYear, ok := h.byCountryYear[country]; ok {
		return byYear[year]
	}
	return platformPool{}
}

// hwStreams bundles the hardware-only per-shop RNG streams.
type hwStreams struct {
	vol, timeOfDay, plat, model, cond, attach, chann, pay, stock, trade *rand.Rand
	custLink, custPick                                                  *rand.Rand
	// V1.21.2 — accessory streams.
	accVol, accTime, accAttach *rand.Rand
	// V1.22 — price jitter.
	priceJit *rand.Rand
}

// filterKind returns the subset of models that are (or are not) accessories.
func filterKind(models []hardware.Model, accessory bool) []hardware.Model {
	out := make([]hardware.Model, 0, len(models))
	for _, m := range models {
		if (m.Kind == "accessory") == accessory {
			out = append(out, m)
		}
	}
	return out
}

// emitHardwareTransaction builds one console/hardware-purchase transaction:
// a hardware line (+ attach game lines) and an optional used-console
// trade-in. Reuses the software helpers for channel/payment/linkage/till.
func emitHardwareTransaction(
	ctr *txCounters,
	shop *shops.Shop,
	shopOpen, day time.Time,
	secondOfDay int,
	minorUnit int,
	hw *hardware.Index,
	hwElig *hardwareEligibility,
	cat *catalog.Index,
	country string,
	inv *inventoryTracker,
	r hwStreams,
	cust CustomerPicker,
	v *v118,
	accessoryOnly bool, // V1.21.2: standalone accessory purchase (no console)
	emit func(Transaction),
) {
	occurredAt := day.Add(time.Duration(secondOfDay) * time.Second)
	precision := policy.SignupPrecisionForYear(occurredAt.Year())
	occurredAt = coarsenToPrecision(occurredAt, precision)
	if occurredAt.Before(shopOpen) {
		occurredAt = shopOpen
	}
	year := occurredAt.Year()
	sourceSys := transactionSourceSystem(year)

	// Pick the platform whose hardware is hot this year (launch-shape × region).
	platform, ok := hwElig.pool(country, year).sample(r.plat)
	if !ok {
		return
	}
	allModels := hw.AvailableModels(platform, country, occurredAt)
	model, ok := pickHardwareModel(filterKind(allModels, accessoryOnly), occurredAt, r.model)
	if !ok {
		return // no console (or accessory) launched in this region yet
	}

	// Condition: consoles can be new/used; accessories are sold new.
	condition := policy.ConditionNew
	if !accessoryOnly && r.cond.Float64() >= policy.HardwareNewVsUsedSplit(year) {
		condition = sampleUsedCondition(r.cond)
	}

	currency := shop.CurrencyCode
	fireSaleRate := policy.FireSaleDiscountRate(occurredAt)
	txTaxRate := policy.SalesTaxRate(country, year) // V1.22: per-country/era

	var (
		lines     []Line
		movements []InventoryMovement
		newInv    []InventoryRow
		subtotal  float64
		taxTotal  float64
		discount  float64
		lineNo    int
	)

	addLine := func(releaseID, hardwareID int64, cond string, unitPrice float64, qty int, invID int64, isNew bool) {
		lineNo++
		gross := unitPrice * float64(qty)
		lineDisc := 0.0
		if fireSaleRate > 0 {
			lineDisc = roundTo(gross*fireSaleRate, minorUnit)
		}
		taxable := gross - lineDisc
		lineTax := roundTo(taxable*txTaxRate, minorUnit)
		lineTotal := roundTo(taxable+lineTax, minorUnit)
		l := Line{
			TransactionLineID: *ctr.line,
			LineNumber:        lineNo,
			ReleaseID:         releaseID,
			HardwareID:        hardwareID,
			Condition:         cond,
			Quantity:          qty,
			UnitPrice:         unitPrice,
			LineDiscount:      lineDisc,
			LineTax:           lineTax,
			LineTotal:         lineTotal,
			InventoryID:       invID,
			InventoryNew:      isNew,
		}
		if isNew {
			l.OnHand = onHandSampleFor(cond, r.stock)
			row := InventoryRow{InventoryID: invID, ShopID: shop.ShopID, Condition: cond, OnHand: l.OnHand}
			row.ReleaseID = releaseID
			row.HardwareID = hardwareID
			newInv = append(newInv, row)
		}
		lines = append(lines, l)
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
		discount += lineDisc
	}

	// The hardware line.
	hwPrice := jitterPrice(hardwarePrice(model, occurredAt, condition, currency, minorUnit), minorUnit, r.priceJit)
	hwInvID := HardwareInventoryIDFor(shop.ShopID, model.HardwareID, condition)
	addLine(0, model.HardwareID, condition, hwPrice, 1, hwInvID, inv.touchHardware(model.HardwareID, condition))

	// Console purchases pull games (the attach rate) and often an
	// accessory (extra controller / memory card) into the basket. A
	// standalone accessory purchase is just the accessory.
	if !accessoryOnly {
		attachN := sampleHardwareAttach(r.attach, year)
		for i := 0; i < attachN; i++ {
			rel, ok := cat.SampleForPlatformWeighted(platform, occurredAt, r.attach)
			if !ok {
				break
			}
			basePrice := policy.BasePriceByCurrency[currency]
			if basePrice == 0 {
				basePrice = 50.0
			}
			price := jitterPrice(priceFor(rel, basePrice, year, minorUnit, policy.ConditionNew), minorUnit, r.priceJit)
			invID := InventoryIDFor(shop.ShopID, rel.ReleaseID, policy.ConditionNew)
			addLine(rel.ReleaseID, 0, policy.ConditionNew, price, 1, invID, inv.touch(rel.ReleaseID, policy.ConditionNew))
		}
		if r.accAttach.Float64() < policy.AccessoryAttachProbability(year) {
			if acc, ok := pickHardwareModel(filterKind(allModels, true), occurredAt, r.accAttach); ok {
				price := jitterPrice(hardwarePrice(acc, occurredAt, policy.ConditionNew, currency, minorUnit), minorUnit, r.priceJit)
				invID := HardwareInventoryIDFor(shop.ShopID, acc.HardwareID, policy.ConditionNew)
				addLine(0, acc.HardwareID, policy.ConditionNew, price, 1, invID, inv.touchHardware(acc.HardwareID, policy.ConditionNew))
			}
		}
	}

	if len(lines) == 0 {
		return
	}
	subtotal = roundTo(subtotal, minorUnit)
	taxTotal = roundTo(taxTotal, minorUnit)
	discount = roundTo(discount, minorUnit)
	total := roundTo(subtotal-discount+taxTotal, minorUnit)

	channel := pickChannel(r.chann, year)
	payment := Payment{
		PaymentID:    *ctr.payment,
		Method:       pickPaymentMethod(r.pay, year, channel),
		CurrencyCode: currency,
		Amount:       total,
	}
	*ctr.payment++

	var customerID *int64
	if cust != nil && r.custLink.Float64() < policy.CustomerLinkageProbability(channel, year) {
		if cid, ok := cust.Pick(shop.CountryCode, occurredAt, r.custPick); ok {
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
		CurrencyCode:        currency,
		Subtotal:            subtotal,
		TaxTotal:            taxTotal,
		DiscountTotal:       discount,
		Total:               total,
		SourceSystem:        sourceSys,
		Lines:               lines,
		Payments:            []Payment{payment},
		Movements:           movements,
		NewInventory:        newInv,
	}

	// Used-console trade-in (the upgrade cycle): trade an old box toward
	// the new one. Grants store credit (identified) or settles via offset.
	if !accessoryOnly && r.trade.Float64() < policy.HardwareTradeInProbability(year) {
		emitHardwareTradeIn(ctr, &tx, shop, occurredAt, precision, sourceSys, minorUnit,
			currency, country, year, hw, hwElig, inv, r, v, customerID)
	}

	// Staff on duty for in-store-style channels.
	if v != nil && v.staff != nil && (channel == "in_store" || channel == "click_and_collect") {
		if spellID, ok := v.staff.PickOnDuty(shop.ShopID, occurredAt, v.staffRNG); ok {
			tx.StaffID = &spellID
			if tx.TradeIn != nil {
				tx.TradeIn.StaffID = &spellID
			}
		}
	}
	if customerID != nil && remoteChannels[channel] {
		addrID := *customerID
		tx.ShippingAddressID = &addrID
	}
	if channel == "in_store" {
		tx.TillID = tillIDFor(tx.TransactionID, shop.ShopID, year)
	}
	tx.DeviceID = deviceIDFor(tx.TransactionID, channel, year)
	for i := range tx.Payments {
		tx.Payments[i].ProcessorRef = processorRefFor(tx.Payments[i].PaymentID, tx.Payments[i].Method, year)
	}

	*ctr.tx++
	emit(tx)
}

// emitHardwareTradeIn blends a used-console trade-in onto a hardware
// purchase: one older console, valued used, paid out cash/bank (offset) or
// store credit (ledger grant — never both).
func emitHardwareTradeIn(
	ctr *txCounters, tx *Transaction, shop *shops.Shop,
	occurredAt time.Time, precision, sourceSys string, minorUnit int,
	currency, country string, year int,
	hw *hardware.Index, hwElig *hardwareEligibility,
	inv *inventoryTracker, r hwStreams, v *v118, customerID *int64,
) {
	platform, ok := hwElig.pool(country, year).sample(r.trade)
	if !ok {
		return
	}
	models := hw.AvailableModels(platform, country, occurredAt)
	model, ok := pickHardwareModel(models, occurredAt, r.trade)
	if !ok {
		return
	}
	cond := sampleUsedCondition(r.trade)
	market := hardwarePrice(model, occurredAt, policy.ConditionNew, currency, minorUnit)
	valuation := hardwareTradeInValue(market, cond, minorUnit)
	if valuation <= 0 {
		return
	}
	ti := &TradeIn{
		TradeInID:           *ctr.tradeIn,
		OccurredAt:          formatTimestamp(occurredAt, precision),
		OccurredAtPrecision: precision,
		ShopID:              shop.ShopID,
		CustomerID:          customerID,
		TransactionID:       &tx.TransactionID,
		CurrencyCode:        currency,
		TotalValue:          valuation,
		PayoutMethod:        samplePayoutMethod(r.trade, customerID != nil),
		SourceSystem:        sourceSys,
		Items: []TradeInItem{{
			TradeInItemID: *ctr.tradeInItem,
			HardwareID:    model.HardwareID,
			Condition:     cond,
			Valuation:     valuation,
		}},
	}
	*ctr.tradeIn++
	*ctr.tradeInItem++
	tx.TradeIn = ti

	if ti.PayoutMethod == "store_credit" && customerID != nil {
		// Grant flows through the ledger (and the shop-local balance state,
		// so future purchases can redeem it); offset stays 0 (no double pay).
		tx.TradeInOffset = 0
		if v != nil {
			v.credit.grant(*customerID, valuation)
		}
		tiID := ti.TradeInID
		tx.StoreCreditEvents = append(tx.StoreCreditEvents, StoreCreditEvent{
			LedgerID:      *ctr.ledger,
			CustomerID:    *customerID,
			OccurredAt:    formatTimestamp(occurredAt, precision),
			EventType:     "credit_granted",
			Amount:        valuation,
			CurrencyCode:  currency,
			TradeInID:     &tiID,
			TransactionID: &tx.TransactionID,
			SourceSystem:  sourceSys,
		})
		*ctr.ledger++
	} else {
		offset := valuation
		if offset > tx.Total {
			offset = tx.Total
		}
		tx.TradeInOffset = offset
		remaining := roundTo(tx.Total-offset, minorUnit)
		if remaining <= 0 {
			tx.TradeInOffset = tx.Total
			tx.Payments = nil
		} else if len(tx.Payments) > 0 {
			tx.Payments[0].Amount = remaining
		}
	}

	// Traded-in console arrives as used stock (positive movement).
	invID := HardwareInventoryIDFor(shop.ShopID, model.HardwareID, cond)
	if inv.touchHardware(model.HardwareID, cond) {
		tx.NewInventory = append(tx.NewInventory, InventoryRow{
			InventoryID: invID, ShopID: shop.ShopID,
			HardwareID: model.HardwareID, Condition: cond,
			OnHand: onHandSampleFor(cond, r.stock),
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

// pickHardwareModel weights available models by HardwareModelShape so the
// newest available revision dominates new sales (slim supersedes fat).
func pickHardwareModel(models []hardware.Model, asOf time.Time, r *rand.Rand) (hardware.Model, bool) {
	if len(models) == 0 {
		return hardware.Model{}, false
	}
	cum := make([]float64, len(models))
	var sum float64
	for i, m := range models {
		yrs := asOf.Year() - m.FirstRelease.Year()
		w := policy.HardwareModelShape(yrs)
		if w < 0 {
			w = 0
		}
		sum += w
		cum[i] = sum
	}
	if sum <= 0 {
		return models[r.IntN(len(models))], true
	}
	target := r.Float64() * sum
	idx := sort.SearchFloat64s(cum, target)
	if idx >= len(models) {
		idx = len(models) - 1
	}
	return models[idx], true
}

// hardwarePrice converts a model's USD launch price (price-dropped over its
// life, condition-adjusted) into the shop currency.
func hardwarePrice(m hardware.Model, asOf time.Time, condition, currency string, minorUnit int) float64 {
	usd := m.LaunchUSD
	if usd <= 0 {
		usd = hardwareKindDefaultUSD(m.Kind)
	}
	yearsOnMarket := asOf.Year() - m.FirstRelease.Year()
	if yearsOnMarket < 0 {
		yearsOnMarket = 0
	}
	usd *= policy.HardwarePriceDropCurve(yearsOnMarket)
	if condition != policy.ConditionNew {
		usd *= policy.ConditionPriceMultiplier(condition)
	}
	return roundTo(usd*hwCurrencyRatio(currency), minorUnit)
}

func hwCurrencyRatio(currency string) float64 {
	usd := policy.BasePriceByCurrency["USD"]
	c := policy.BasePriceByCurrency[currency]
	if usd <= 0 || c <= 0 {
		return 1.0
	}
	return c / usd
}

func hardwareKindDefaultUSD(kind string) float64 {
	switch kind {
	case "handheld":
		return 129
	case "computer":
		return 399
	case "accessory":
		return 40
	default: // console
		return 199
	}
}

// hardwareTradeInValue: ~30% of the used resale value (market price ×
// condition multiplier × rate), mirroring the game tradeInValue rate.
func hardwareTradeInValue(market float64, condition string, minorUnit int) float64 {
	const rate = 0.30
	return roundTo(market*policy.ConditionPriceMultiplier(condition)*rate, minorUnit)
}

// sampleHardwareAttach returns how many same-platform game lines a console
// purchase pulls into the basket (mode ~1-2; more in the boom era).
func sampleHardwareAttach(r *rand.Rand, year int) int {
	u := r.Float64()
	c0, c1, c2 := 0.25, 0.65, 0.92
	if year >= 2000 && year <= 2012 {
		c0, c1, c2 = 0.15, 0.55, 0.85
	}
	switch {
	case u < c0:
		return 0
	case u < c1:
		return 1
	case u < c2:
		return 2
	default:
		return 3
	}
}
