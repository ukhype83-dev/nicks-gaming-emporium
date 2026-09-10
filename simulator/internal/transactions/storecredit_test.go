package transactions

import "testing"

func TestCreditStateNeverNegative(t *testing.T) {
	cs := newCreditState()

	if got := cs.redeem(1, 50.00, 2); got != 0 {
		t.Errorf("redeem with no balance: want 0, got %v", got)
	}

	cs.grant(1, 30.00)
	if bal := cs.available(1); bal != 30.00 {
		t.Errorf("balance after grant: want 30, got %v", bal)
	}

	// Redeem capped at balance.
	if got := cs.redeem(1, 50.00, 2); got != 30.00 {
		t.Errorf("redeem capped: want 30, got %v", got)
	}
	if bal := cs.available(1); bal != 0 {
		t.Errorf("balance after full redeem: want 0, got %v", bal)
	}

	// Redeem capped at want.
	cs.grant(1, 19.17)
	if got := cs.redeem(1, 10.00, 2); got != 10.00 {
		t.Errorf("partial redeem: want 10, got %v", got)
	}
	if bal := cs.available(1); bal != 9.17 {
		t.Errorf("residual balance: want 9.17, got %v", bal)
	}

	// Independent customers.
	if got := cs.redeem(2, 5.00, 2); got != 0 {
		t.Errorf("other customer must have no balance, got %v", got)
	}
}

func TestCreditStateZeroMinorUnit(t *testing.T) {
	cs := newCreditState()
	cs.grant(7, 580) // ¥580
	// Want ¥123.45-equivalent? JPY amounts are integral; the clamp
	// guards against fractional redemption requests.
	if got := cs.redeem(7, 99.5, 0); got != 99 {
		t.Errorf("JPY redeem clamps to whole units: want 99, got %v", got)
	}
	if bal := cs.available(7); bal != 481 {
		t.Errorf("JPY residual: want 481, got %v", bal)
	}
}

func TestCreditStateFloatStability(t *testing.T) {
	cs := newCreditState()
	// Hundreds of small odd-cent grants/redeems must stay exact.
	for i := 0; i < 500; i++ {
		cs.grant(3, 0.03)
	}
	if bal := cs.available(3); bal != 15.00 {
		t.Errorf("500 x 0.03: want exactly 15.00, got %v", bal)
	}
	for i := 0; i < 100; i++ {
		if got := cs.redeem(3, 0.15, 2); got != 0.15 {
			t.Fatalf("iteration %d: want 0.15, got %v", i, got)
		}
	}
	if bal := cs.available(3); bal != 0 {
		t.Errorf("drained balance: want 0, got %v", bal)
	}
}
