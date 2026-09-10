// V1.24.0 — the fraud pocket (Track 800, Exhibit H: "The Protégé").
//
// One shop (policy.FraudShopCode), one manager (spell_id ==
// policy.FraudManagerSpellID, seeded by hr.emitFraudManager), eighteen
// months (policy.FraudWindowStart..End) of keyed cash refunds against
// stale sales, skimmed from the drawer. See the policy.go block for the
// full list of queryable signatures.
//
// Mechanics: the fraud shop's generator keeps a small buffer of recent
// UNREFUNDED small-ticket sales (the organic return roll runs first, so
// a parent can never be refunded twice). Each in-window day, a Poisson
// count of fraud refunds is drawn from a dedicated RNG namespace
// ("tx/shop/<id>/fraud" — additive; no existing stream is perturbed)
// and issued against buffered parents 20-120 days old, timestamped in
// the closing hour, always cash, always carrying the manager's spell_id
// in staff_id, and never restocking inventory (no goods came back —
// there were no goods).

package transactions

import (
	"math/rand/v2"
	"time"

	"emporium/internal/policy"
	"emporium/internal/shops"
)

// fraudParent is the trimmed snapshot of an eligible refund target.
type fraudParent struct {
	txID       int64
	at         time.Time
	customerID *int64
	line       Line
	total      float64
}

// fraudState exists only on the fraud shop's generator (nil elsewhere).
type fraudState struct {
	rng *rand.Rand
	buf []fraudParent
}

func newFraudState(r *rand.Rand) *fraudState {
	return &fraudState{rng: r, buf: make([]fraudParent, 0, 1024)}
}

// bufferWindowStart is the earliest sale date worth buffering: a fraud
// refund issued on WindowStart can reference a sale FraudLagDaysMax old.
func fraudBufferWindowStart() time.Time {
	return policy.FraudWindowStart.AddDate(0, 0, -policy.FraudLagDaysMax)
}

// maybeBuffer records a just-emitted, organically-unrefunded sale as a
// potential fraud target. Eligibility mirrors what a careful embezzler
// picks: a real receipt, paid in a refundable tender, small enough to
// sit under the review threshold nobody looks at.
func (f *fraudState) maybeBuffer(tx *Transaction, at time.Time) {
	if at.Before(fraudBufferWindowStart()) || at.After(policy.FraudWindowEnd) {
		return
	}
	if len(tx.Lines) == 0 || len(tx.Payments) == 0 {
		return
	}
	if tx.OriginalTransactionID != nil || tx.Total <= 0 {
		return
	}
	pay := tx.Payments[0]
	if pay.Method == "store_credit" || pay.Amount <= 0 {
		return
	}
	if tx.Total > policy.FraudMaxRefundUSD {
		return
	}
	f.buf = append(f.buf, fraudParent{
		txID:       tx.TransactionID,
		at:         at,
		customerID: tx.CustomerID,
		line:       tx.Lines[0],
		total:      tx.Total,
	})
}

// evictBefore drops buffered parents too old to reference from `day`.
func (f *fraudState) evictBefore(day time.Time) {
	cutoff := day.AddDate(0, 0, -policy.FraudLagDaysMax)
	keep := f.buf[:0]
	for _, p := range f.buf {
		if !p.at.Before(cutoff) {
			keep = append(keep, p)
		}
	}
	f.buf = keep
}

// emitFraudDay issues the day's keyed refunds. Called once per shop-day
// on the fraud shop; no-ops outside the fraud window.
func (f *fraudState) emitFraudDay(
	ctr *txCounters,
	shop *shops.Shop,
	day time.Time,
	minorUnit int,
	emit func(Transaction),
) {
	if day.Before(policy.FraudWindowStart) || day.After(policy.FraudWindowEnd) {
		return
	}
	f.evictBefore(day)
	n := samplePoisson(f.rng, policy.FraudRefundsPerDayMean)
	for i := 0; i < n; i++ {
		// Collect candidates old enough to be safely stale.
		minAge := day.AddDate(0, 0, -policy.FraudLagDaysMin)
		candidates := make([]int, 0, len(f.buf))
		for idx, p := range f.buf {
			if p.at.Before(minAge) {
				candidates = append(candidates, idx)
			}
		}
		if len(candidates) == 0 {
			return
		}
		pick := candidates[f.rng.IntN(len(candidates))]
		parent := f.buf[pick]
		// Remove the used parent (swap-delete keeps this O(1); order of
		// the buffer is irrelevant — selection is by age filter + RNG).
		f.buf[pick] = f.buf[len(f.buf)-1]
		f.buf = f.buf[:len(f.buf)-1]

		f.emitKeyedRefund(ctr, shop, parent, day, minorUnit, emit)
	}
}

// emitKeyedRefund mirrors emitReturn's shape with the fraud signatures:
// closing-hour timestamp, cash tender regardless of the original,
// staff_id stamped with the manager's spell, and NO inventory movement.
func (f *fraudState) emitKeyedRefund(
	ctr *txCounters,
	shop *shops.Shop,
	parent fraudParent,
	day time.Time,
	minorUnit int,
	emit func(Transaction),
) {
	ln := parent.line
	if ln.LineTotal <= 0 {
		return
	}
	retAt := time.Date(day.Year(), day.Month(), day.Day(),
		policy.FraudHourOfDay, 0, 0, 0, time.UTC).
		Add(time.Duration(f.rng.IntN(3600)) * time.Second)
	retPrecision := policy.SignupPrecisionForYear(retAt.Year())
	retAt = coarsenToPrecision(retAt, retPrecision)
	sourceSys := transactionSourceSystem(retAt.Year())

	refundTotal := roundTo(ln.LineTotal, minorUnit)
	refundTax := roundTo(ln.LineTax, minorUnit)
	refundSub := roundTo(ln.UnitPrice*float64(ln.Quantity)-ln.LineDiscount, minorUnit)
	parentID := parent.txID
	staffSpell := int64(policy.FraudManagerSpellID)

	rtx := Transaction{
		TransactionID:         *ctr.tx,
		OccurredAt:            formatTimestamp(retAt, retPrecision),
		OccurredAtPrecision:   retPrecision,
		ShopID:                shop.ShopID,
		Channel:               "in_store",
		CustomerID:            parent.customerID,
		StaffID:               &staffSpell, // the keyed-refund authorization log
		CurrencyCode:          shop.CurrencyCode,
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
			LineDiscount:      ln.LineDiscount,
			LineTax:           -refundTax,
			LineTotal:         -refundTotal,
		}},
		Payments: []Payment{{
			PaymentID:    *ctr.payment,
			Method:       "cash", // the skim: cash out, whatever the original tender
			CurrencyCode: shop.CurrencyCode,
			Amount:       -refundTotal,
		}},
		// No Movements: nothing was restocked. There were no goods.
	}
	rtx.Payments[0].ProcessorRef = processorRefFor(rtx.Payments[0].PaymentID, "cash", retAt.Year())
	rtx.TillID = tillIDFor(rtx.TransactionID, shop.ShopID, retAt.Year())
	*ctr.line++
	*ctr.payment++
	*ctr.tx++
	emit(rtx)
}
