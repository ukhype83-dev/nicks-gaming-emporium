package transactions

import (
	"math/rand/v2"
	"regexp"
	"testing"

	"emporium/internal/rng"
)

func newTestRand() *rand.Rand { return rng.Derive(42, "test/payout") }

func TestTillIDEraGrading(t *testing.T) {
	if got := tillIDFor(123456, 42, 2007); got != nil {
		t.Errorf("pre-2008: want nil, got %q", *got)
	}
	transitional := tillIDFor(123456, 42, 2012)
	if transitional == nil || !regexp.MustCompile(`^[1-4]$`).MatchString(*transitional) {
		t.Errorf("2008-2015: want bare digit 1-4, got %v", transitional)
	}
	native := tillIDFor(123456, 42, 2016)
	if native == nil || !regexp.MustCompile(`^T[1-4]$`).MatchString(*native) {
		t.Errorf("2016: want T1-T4, got %v", native)
	}
	// Determinism.
	a, b := tillIDFor(99, 7, 2012), tillIDFor(99, 7, 2012)
	if *a != *b {
		t.Errorf("till id must be deterministic: %q vs %q", *a, *b)
	}
}

func TestDeviceIDFormat(t *testing.T) {
	if got := deviceIDFor(555, "online", 2009); got != nil {
		t.Errorf("pre-2010: want nil, got %q", *got)
	}
	if got := deviceIDFor(555, "in_store", 2014); got != nil {
		t.Errorf("in_store: want nil, got %q", *got)
	}
	web := deviceIDFor(555, "online", 2014)
	if web == nil || !regexp.MustCompile(`^web-[0-9a-f]{8}$`).MatchString(*web) {
		t.Errorf("online: want web-xxxxxxxx, got %v", web)
	}
	app := deviceIDFor(555, "mobile_app", 2014)
	if app == nil || !regexp.MustCompile(`^app-[0-9a-f]{8}$`).MatchString(*app) {
		t.Errorf("mobile_app: want app-xxxxxxxx, got %v", app)
	}
	if *web == (*app)[3:] {
		t.Error("prefix must be part of the token, not the only difference")
	}
}

func TestProcessorRefFormat(t *testing.T) {
	if got := processorRefFor(42, "card_emv", 2007); got != nil {
		t.Errorf("pre-2008: want nil, got %q", *got)
	}
	if got := processorRefFor(42, "cash", 2012); got != nil {
		t.Errorf("cash: want nil, got %q", *got)
	}
	ref := processorRefFor(42, "card_emv", 2012)
	if ref == nil || !regexp.MustCompile(`^PR-[0-9A-F]{10}$`).MatchString(*ref) {
		t.Errorf("card_emv 2012: want PR-XXXXXXXXXX (13 chars), got %v", ref)
	}
	if len(*ref) != 13 {
		t.Errorf("processor_ref must be 13 chars, got %d", len(*ref))
	}
	// Distinct payments get distinct refs (overwhelmingly).
	other := processorRefFor(43, "card_emv", 2012)
	if *ref == *other {
		t.Error("adjacent payment ids must not collide")
	}
}

func TestSamplePayoutMethodSplit(t *testing.T) {
	// Statistical sanity over a derived stream: anonymous never gets
	// store_credit; identified gets all three in plausible shares.
	r := newTestRand()
	counts := map[string]int{}
	for i := 0; i < 10000; i++ {
		counts[samplePayoutMethod(r, false)]++
	}
	if counts["store_credit"] != 0 {
		t.Errorf("anonymous must never get store_credit, got %d", counts["store_credit"])
	}
	counts = map[string]int{}
	for i := 0; i < 10000; i++ {
		counts[samplePayoutMethod(r, true)]++
	}
	if counts["store_credit"] < 4500 || counts["store_credit"] > 5500 {
		t.Errorf("identified store_credit share ~50%%, got %d/10000", counts["store_credit"])
	}
	if counts["cash"] < 3000 || counts["cash"] > 4000 {
		t.Errorf("identified cash share ~35%%, got %d/10000", counts["cash"])
	}
}
