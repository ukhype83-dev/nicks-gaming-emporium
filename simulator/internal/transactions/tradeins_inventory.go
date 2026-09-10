// Inventory + trade-in records emitted alongside each transaction:
//
//   - retail.inventory             — one row per (shop, release, condition)
//     touched by a sale or trade-in. on_hand is a snapshot value
//     (~10-200 units), not a strict running balance — running stock
//     would require holding tens of millions of state values across
//     the entire load and is V1.7 work.
//   - retail.inventory_movements   — one row per sale line (negative
//     quantity) + one per trade-in item (positive quantity).
//   - retail.trade_ins             — ~8% of transactions are blended
//     with a trade-in; standalone trade-ins are V1.7.
//   - retail.trade_in_items        — 1-3 per trade-in.
//   - retail.store_credit_ledger   — credit_granted event per trade-in
//     paid out as store_credit; credit_used events are V1.7 (would
//     need to model customer credit balance and consumption).
//
// inventory_id is deterministic per (shop, release, condition) so
// movements can reference inventory without a separate lookup pass:
//
//   inventory_id = shop_id × 10_000_000 + release_id × 10 + cond_idx
//
// Each generation flush produces:
//   - any newly-encountered inventory rows for tuples seen this batch
//   - movements / trade-ins / items / ledger entries from this batch
package transactions

import (
	"math/rand/v2"

	"emporium/internal/policy"
)

// --------------------------------------------------------------------
// Type definitions
// --------------------------------------------------------------------

// InventoryMovement maps to retail.inventory_movements.
type InventoryMovement struct {
	MovementID          int64   `json:"movement_id"`
	InventoryID         int64   `json:"inventory_id"`
	OccurredAt          string  `json:"occurred_at"`
	OccurredAtPrecision string  `json:"occurred_at_precision"`
	MovementType        string  `json:"movement_type"`
	Quantity            int     `json:"quantity"`
	ReferenceType       *string `json:"reference_type"`
	ReferenceID         *int64  `json:"reference_id"`
	SourceSystem        string  `json:"source_system"`
}

// InventoryRow maps to retail.inventory. Emitted lazily on first
// encounter of each (shop, release, condition) tuple.
type InventoryRow struct {
	InventoryID    int64   `json:"inventory_id"`
	ShopID         int64   `json:"shop_id"`
	ReleaseID      int64   `json:"release_id"`  // 0 on a hardware stock row
	HardwareID     int64   `json:"hardware_id"` // V1.21; 0 on a software stock row
	Condition      string  `json:"condition"`
	OnHand         int     `json:"on_hand"`
	OnOrder        int     `json:"on_order"`
	Reserved       int     `json:"reserved"`
	LastMovementAt *string `json:"last_movement_at"`
}

// TradeIn maps to retail.trade_ins. Emitted blended with a parent
// transaction (~8% probability). Standalone trade-ins (no linked
// purchase) are V1.7.
type TradeIn struct {
	TradeInID           int64   `json:"trade_in_id"`
	OccurredAt          string  `json:"occurred_at"`
	OccurredAtPrecision string  `json:"occurred_at_precision"`
	ShopID              int64   `json:"shop_id"`
	CustomerID          *int64  `json:"customer_id"`
	StaffID             *int64  `json:"staff_id"`
	TransactionID       *int64  `json:"transaction_id"`
	CurrencyCode        string  `json:"currency_code"`
	TotalValue          float64 `json:"total_value"`
	PayoutMethod        string  `json:"payout_method"`
	SourceSystem        string  `json:"source_system"`
	Items               []TradeInItem `json:"items"`
}

// TradeInItem maps to retail.trade_in_items.
type TradeInItem struct {
	TradeInItemID int64   `json:"trade_in_item_id"`
	ReleaseID     int64   `json:"release_id"`  // 0 on a hardware trade-in item
	HardwareID    int64   `json:"hardware_id"` // V1.21; 0 on a software trade-in item
	Condition     string  `json:"condition"`
	Valuation     float64 `json:"valuation"`
	Notes         *string `json:"notes"`
}

// StoreCreditEvent maps to retail.store_credit_ledger. V1.18.0 emits
// both sides: credit_granted (+amount; store_credit trade-in payouts,
// identified customers only, trade_in_id + transaction_id set) and
// credit_used (-amount; redemptions on later purchases at the SAME
// shop, transaction_id only). Balances are shop-local by design —
// see the ledger DDL comment in schema_v1_sqlserver.sql.
type StoreCreditEvent struct {
	LedgerID      int64   `json:"ledger_id"`
	CustomerID    int64   `json:"customer_id"` // V1.18.0 — grants/uses are identified by construction
	OccurredAt    string  `json:"occurred_at"`
	EventType     string  `json:"event_type"`
	Amount        float64 `json:"amount"`
	CurrencyCode  string  `json:"currency_code"`
	TradeInID     *int64  `json:"trade_in_id"`
	TransactionID *int64  `json:"transaction_id"`
	SourceSystem  string  `json:"source_system"`
}

// --------------------------------------------------------------------
// Inventory ID assignment
// --------------------------------------------------------------------

// conditionIndex maps the canonical condition vocabulary to a small
// integer for deterministic inventory_id derivation. Order is stable
// — moving entries here is a breaking change.
var conditionIndex = map[string]int{
	"new":        0,
	"used_mint":  1,
	"used_good":  2,
	"used_fair":  3,
	"used_loose": 4,
}

// InventoryIDFor computes the stable inventory_id for the given
// (shop, release, condition) tuple. Sparse but unique within BIGINT.
//
// Layout: shop_id × 10^7 + release_id × 10 + cond_idx. With shop_id
// up to ~1000 and release_id up to ~100K, max value ~1.001×10^10
// — well within BIGINT range (9.2×10^18).
func InventoryIDFor(shopID, releaseID int64, condition string) int64 {
	idx, ok := conditionIndex[condition]
	if !ok {
		idx = 0
	}
	return shopID*10_000_000 + releaseID*10 + int64(idx)
}

// hardwareInventoryIDBase keeps hardware inventory_ids in a high band
// (10^15+) that can't collide with software inventory_ids (≤ ~1.001×10^10).
const hardwareInventoryIDBase = 1_000_000_000_000_000 // 10^15

// HardwareInventoryIDFor computes the stable inventory_id for a (shop,
// hardware, condition) tuple. V1.21.0. Layout: base + shop×10^5 +
// hardware_id×10 + cond_idx (hardware_id ≤ ~90, shop ≤ ~10K).
func HardwareInventoryIDFor(shopID, hardwareID int64, condition string) int64 {
	idx, ok := conditionIndex[condition]
	if !ok {
		idx = 0
	}
	return hardwareInventoryIDBase + shopID*100_000 + hardwareID*10 + int64(idx)
}

// --------------------------------------------------------------------
// Inventory tracker — one per shop generation
// --------------------------------------------------------------------

// inventoryTracker remembers which (shop, release, condition) tuples
// have already had an inventory row emitted, so we don't double-insert
// on subsequent sales of the same SKU. Per-shop scope: keys are
// (release_id, condition_index) packed into one int64 to avoid map[]
// allocation overhead.
type inventoryTracker struct {
	seen map[int64]struct{}
}

func newInventoryTracker() *inventoryTracker {
	return &inventoryTracker{seen: make(map[int64]struct{}, 4096)}
}

// touch returns true if this (release, condition) pair is newly
// encountered for this shop (caller should emit a new inventory row).
func (t *inventoryTracker) touch(releaseID int64, condition string) bool {
	idx, ok := conditionIndex[condition]
	if !ok {
		idx = 0
	}
	key := releaseID*10 + int64(idx)
	if _, exists := t.seen[key]; exists {
		return false
	}
	t.seen[key] = struct{}{}
	return true
}

// touchHardware is the hardware counterpart to touch, using a negative key
// space so (hardware, condition) tuples never collide with (release,
// condition) tuples in the same per-shop tracker. V1.21.0.
func (t *inventoryTracker) touchHardware(hardwareID int64, condition string) bool {
	idx, ok := conditionIndex[condition]
	if !ok {
		idx = 0
	}
	key := -(hardwareID*10 + int64(idx)) - 1
	if _, exists := t.seen[key]; exists {
		return false
	}
	t.seen[key] = struct{}{}
	return true
}

// --------------------------------------------------------------------
// Trade-in valuation + condition
// --------------------------------------------------------------------

// tradeInProbability returns the per-transaction roll for "this
// transaction is blended with a trade-in." Era-aware: trade-ins were
// rare in the cash-only 1980s, ramped up as preowned culture grew.
func tradeInProbability(year int) float64 {
	switch {
	case year <= 1992:
		return 0.02
	case year <= 2000:
		return 0.05
	case year <= 2010:
		return 0.10
	case year <= 2020:
		return 0.13
	default:
		return 0.18 // post-2020 retro pivot — used / trade-in dominant
	}
}

// payoutMethodWeights — distribution per §9.14.6.
//
// V1.10 removed `store_credit` from the payout pool because the
// ledger requires customer_id and V1 trade-ins had none — and
// promised to re-introduce it once trade_ins.customer_id was
// populated. V1.18.0 keeps that promise: identified customers (the
// blended sale carries a customer_id) skew heavily toward store
// credit — the credit account was WHY customers registered (§9.12),
// and real trade-in programmes pay a credit premium. Anonymous
// walk-ins keep the exact V1.10 distribution AND draw pattern (one
// Float64), so anonymous trade-ins remain byte-identical to v1.17.
//
//	anonymous : cash 0.75 | bank_transfer 0.25
//	identified: store_credit 0.50 | cash 0.35 | bank_transfer 0.15
func samplePayoutMethod(r *rand.Rand, identified bool) string {
	u := r.Float64()
	if !identified {
		if u < 0.75 {
			return "cash"
		}
		return "bank_transfer"
	}
	switch {
	case u < 0.50:
		return "store_credit"
	case u < 0.85:
		return "cash"
	default:
		return "bank_transfer"
	}
}

// sampleTradeInCondition — distribution of incoming trade-in
// condition per §9.14.5. No 'new' on inflow (people don't trade in
// brand-new sealed copies, except as fraud which is V1.7+).
func sampleTradeInCondition(r *rand.Rand) string {
	u := r.Float64()
	switch {
	case u < 0.25:
		return policy.ConditionUsedMint
	case u < 0.70:
		return policy.ConditionUsedGood
	case u < 0.92:
		return policy.ConditionUsedFair
	default:
		return policy.ConditionUsedLoose
	}
}

// tradeInValue computes the per-item trade-in valuation. Roughly 30%
// of current-new equivalent price × condition multiplier. Real shops
// pay ~25-40% of the implied retail value.
func tradeInValue(basePrice, eraFactor, ageDiscount float64, condition string, minorUnit int) float64 {
	const tradeInRate = 0.30
	raw := basePrice * eraFactor * ageDiscount * policy.ConditionPriceMultiplier(condition) * tradeInRate
	return roundTo(raw, minorUnit)
}

// onHandSampleFor returns a plausible "currently in stock" count per
// condition. Shops carry more new units of popular SKUs and fewer
// used copies in any given grade.
func onHandSampleFor(condition string, r *rand.Rand) int {
	switch condition {
	case policy.ConditionNew:
		return 5 + r.IntN(40) // 5-44
	case policy.ConditionUsedMint:
		return 1 + r.IntN(5)
	case policy.ConditionUsedGood:
		return 2 + r.IntN(8)
	case policy.ConditionUsedFair:
		return 1 + r.IntN(4)
	case policy.ConditionUsedLoose:
		return r.IntN(3)
	}
	return r.IntN(10)
}
