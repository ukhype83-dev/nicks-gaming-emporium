package transactions

import (
	"testing"
	"time"

	"emporium/internal/hardware"
	"emporium/internal/policy"
	"emporium/internal/rng"
)

func TestHardwarePriceDropsOverLife(t *testing.T) {
	m := hardware.Model{
		LaunchUSD:    299,
		Kind:         "console",
		FirstRelease: time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	launch := time.Date(2000, 6, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2005, 6, 1, 0, 0, 0, 0, time.UTC)

	p0 := hardwarePrice(m, launch, policy.ConditionNew, "USD", 2)
	p5 := hardwarePrice(m, late, policy.ConditionNew, "USD", 2)
	if !(p0 > p5) {
		t.Errorf("hardware price should drop over life: launch %.0f vs +5y %.0f", p0, p5)
	}
	if p0 < 250 || p0 > 320 {
		t.Errorf("launch price %.0f not ≈ $299", p0)
	}
	if pu := hardwarePrice(m, launch, policy.ConditionUsedGood, "USD", 2); !(pu < p0) {
		t.Errorf("used %.0f should be cheaper than new %.0f", pu, p0)
	}
	// JPY base is ¥6000 vs $60 → ×100; a $299 console should be ~¥29,900.
	if pjpy := hardwarePrice(m, launch, policy.ConditionNew, "JPY", 0); pjpy < p0*50 {
		t.Errorf("JPY price %.0f should be ~100× the USD price %.0f", pjpy, p0)
	}
}

func TestSampleHardwareAttachRange(t *testing.T) {
	r := rng.Derive(42, "test/attach")
	sum := 0
	for i := 0; i < 2000; i++ {
		n := sampleHardwareAttach(r, 2005)
		if n < 0 || n > 3 {
			t.Fatalf("attach count out of range [0,3]: %d", n)
		}
		sum += n
	}
	mean := float64(sum) / 2000.0
	if mean < 1.0 || mean > 2.2 {
		t.Errorf("boom-era mean attach %.2f outside expected ~1.3-1.6", mean)
	}
}

// V1.22 — price jitter is bounded ±priceJitterPct and ~mean-1.0 (revenue-
// neutral in expectation); promo markdown is either 0 or in [0.10,0.30].
func TestJitterAndPromo(t *testing.T) {
	r := rng.Derive(42, "test/jitter")
	var sum float64
	const n = 20000
	for i := 0; i < n; i++ {
		p := jitterPrice(100.0, 2, r)
		if p < 100.0*(1-priceJitterPct)-0.01 || p > 100.0*(1+priceJitterPct)+0.01 {
			t.Fatalf("jittered price %.2f outside ±%.0f%% of 100", p, priceJitterPct*100)
		}
		sum += p
	}
	mean := sum / n
	if mean < 99.0 || mean > 101.0 {
		t.Errorf("jitter mean %.2f should be ~100 (symmetric/neutral)", mean)
	}

	rp := rng.Derive(42, "test/promo")
	for i := 0; i < 5000; i++ {
		m := promoMarkdown(2007, rp)
		if m != 0 && (m < 0.10 || m > 0.30) {
			t.Fatalf("promo markdown %.3f should be 0 or in [0.10,0.30]", m)
		}
	}
}

func TestHardwareInventoryIDDistinctFromSoftware(t *testing.T) {
	// A software inventory_id (shop×10^7+release×10+cond) must never collide
	// with a hardware one (10^15 band).
	sw := InventoryIDFor(7000, 99999, "new")
	hwid := HardwareInventoryIDFor(7000, 90, "new")
	if hwid <= sw || hwid < hardwareInventoryIDBase {
		t.Errorf("hardware inventory_id %d should sit in the 10^15 band above software %d", hwid, sw)
	}
}
