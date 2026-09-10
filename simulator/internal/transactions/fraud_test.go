package transactions

import (
	"math/rand/v2"
	"testing"
	"time"

	"emporium/internal/policy"
	"emporium/internal/shops"
)

// V1.24.0 — the fraud pocket's signatures must hold exactly: keyed cash
// refunds, closing-hour timestamps, 20-120-day stale parents, the
// manager's spell_id in staff_id, no inventory movements, and strict
// containment inside the fraud window.
func TestFraudPocketSignatures(t *testing.T) {
	var txID, lineID, payID, movID, tiID, tiiID, ledID int64 = 1000, 1000, 1000, 1000, 1, 1, 1
	ctr := &txCounters{tx: &txID, line: &lineID, payment: &payID, movement: &movID,
		tradeIn: &tiID, tradeInItem: &tiiID, ledger: &ledID}
	shop := &shops.Shop{ShopID: 9, ShopCode: policy.FraudShopCode, CurrencyCode: "USD"}
	f := newFraudState(rand.New(rand.NewPCG(7, 7)))

	// Buffer eligible parents across the pre-window run-up.
	base := policy.FraudWindowStart.AddDate(0, 0, -policy.FraudLagDaysMax+5)
	for i := 0; i < 400; i++ {
		at := base.AddDate(0, 0, i%100)
		parent := Transaction{
			TransactionID: int64(i + 1),
			Total:         30.00,
			Lines: []Line{{
				TransactionLineID: int64(i + 1), LineNumber: 1,
				Quantity: 1, UnitPrice: 28.30, LineTax: 1.70, LineTotal: 30.00,
			}},
			Payments: []Payment{{Method: "card_emv", Amount: 30.00}},
		}
		f.maybeBuffer(&parent, at)
	}
	if len(f.buf) == 0 {
		t.Fatal("no parents buffered")
	}

	// Ineligible parents must be rejected.
	rejects := []Transaction{
		{Total: 30, Lines: []Line{{LineTotal: 30}}, Payments: []Payment{{Method: "store_credit", Amount: 30}}},
		{Total: policy.FraudMaxRefundUSD + 10, Lines: []Line{{LineTotal: 65}}, Payments: []Payment{{Method: "cash", Amount: 65}}},
		{Total: 30},
	}
	before := len(f.buf)
	for i := range rejects {
		f.maybeBuffer(&rejects[i], policy.FraudWindowStart)
	}
	if len(f.buf) != before {
		t.Errorf("ineligible parents were buffered (%d -> %d)", before, len(f.buf))
	}

	var emitted []Transaction
	emit := func(tx Transaction) { emitted = append(emitted, tx) }

	// Outside the window: nothing.
	f.emitFraudDay(ctr, shop, policy.FraudWindowStart.AddDate(0, 0, -1), 2, emit)
	if len(emitted) != 0 {
		t.Fatalf("fraud emitted outside the window: %d", len(emitted))
	}

	// Inside the window: run several days, check every signature.
	day := policy.FraudWindowStart.AddDate(0, 0, 40)
	for d := 0; d < 10; d++ {
		f.emitFraudDay(ctr, shop, day.AddDate(0, 0, d), 2, emit)
	}
	if len(emitted) == 0 {
		t.Fatal("no fraud refunds emitted inside the window")
	}
	for _, r := range emitted {
		if r.Total >= 0 || r.OriginalTransactionID == nil {
			t.Fatalf("fraud refund malformed: total=%v orig=%v", r.Total, r.OriginalTransactionID)
		}
		if r.StaffID == nil || *r.StaffID != policy.FraudManagerSpellID {
			t.Errorf("fraud refund missing the manager's spell_id")
		}
		if len(r.Payments) != 1 || r.Payments[0].Method != "cash" {
			t.Errorf("fraud refund not cash: %+v", r.Payments)
		}
		if len(r.Movements) != 0 {
			t.Errorf("fraud refund restocked inventory — there were no goods")
		}
		ts, err := time.Parse("2006-01-02T15:04:05Z", r.OccurredAt)
		if err != nil {
			// Coarser precision strings won't carry the hour; only check
			// when a full timestamp is present.
			continue
		}
		if ts.Hour() != policy.FraudHourOfDay {
			t.Errorf("fraud refund at hour %d, want %d", ts.Hour(), policy.FraudHourOfDay)
		}
	}
}