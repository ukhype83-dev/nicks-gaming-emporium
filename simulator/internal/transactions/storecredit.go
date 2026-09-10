// V1.18.0 — shop-local store-credit balance machine.
//
// Store credit is SHOP-LOCAL by design: a grant at shop S can only be
// redeemed at shop S. That makes balances deterministic under the
// parallel shard model (one goroutine per shop, chronological within
// the shop) with zero cross-worker coordination — and it's
// narratively sound: shop-issued vouchers, never quite unified even
// after the 2011 loyalty merge. Unredeemed balances are breakage;
// after Chapter 11, credit holders were unsecured creditors and those
// balances died with the company. No credit_expired events in V1.
//
// Balances are tracked in integer hundredths of the shop's currency
// unit ("cents") — no float drift across grant/redeem chains. For
// zero-minor-unit currencies (JPY, KRW) redemptions are clamped to
// whole units so emitted amounts stay representable.

package transactions

import "math"

type creditState struct {
	balance map[int64]int64 // customer_id -> cents
}

func newCreditState() *creditState {
	return &creditState{balance: make(map[int64]int64)}
}

func toCents(amount float64) int64 {
	return int64(math.Round(amount * 100))
}

func fromCents(c int64) float64 {
	return float64(c) / 100
}

// grant adds a store-credit payout to the customer's balance at this
// shop.
func (cs *creditState) grant(customerID int64, amount float64) {
	if amount <= 0 {
		return
	}
	cs.balance[customerID] += toCents(amount)
}

// available reports the customer's current balance at this shop.
func (cs *creditState) available(customerID int64) float64 {
	return fromCents(cs.balance[customerID])
}

// redeem deducts and returns the amount actually redeemed:
// min(balance, want), clamped to whole currency units when
// minorUnit == 0. Returns 0 when nothing can be redeemed.
func (cs *creditState) redeem(customerID int64, want float64, minorUnit int) float64 {
	if want <= 0 {
		return 0
	}
	bal := cs.balance[customerID]
	if bal <= 0 {
		return 0
	}
	r := toCents(want)
	if r > bal {
		r = bal
	}
	if minorUnit == 0 {
		r -= r % 100
	}
	if r <= 0 {
		return 0
	}
	cs.balance[customerID] = bal - r
	return fromCents(r)
}
