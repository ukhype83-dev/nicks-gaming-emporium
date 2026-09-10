package policy

import (
	"math"
	"testing"
)

// V1.19 — the re-weighted shop estate must still sum to exactly 1.0,
// be US-heavy (~55%), and carry only a token JP/KR presence. Order is
// load-bearing, so also pin the anchored positions.
func TestShopSharesSumToOne(t *testing.T) {
	var sum float64
	got := map[string]float64{}
	for _, s := range ShopShares {
		sum += s.Share
		got[s.Country] = s.Share
	}
	if math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("ShopShares sum = %.12f, want 1.0", sum)
	}
	for country, want := range map[string]float64{"US": 0.550, "JP": 0.015, "KR": 0.005} {
		if math.Abs(got[country]-want) > 1e-9 {
			t.Errorf("%s share = %.4f, want %.4f", country, got[country], want)
		}
	}
	if ShopShares[0].Country != "US" {
		t.Errorf("US must remain slot 0 (load-bearing order), got %s", ShopShares[0].Country)
	}
	if len(ShopShares[0].AnchorCities) == 0 || ShopShares[0].AnchorCities[0].City != "Chicago" {
		t.Errorf("Chicago must remain US anchor slot 0 (founding shop), got %+v", ShopShares[0].AnchorCities)
	}
}

// V1.19 — bonus fraction is zero for entry level, monotonically
// non-decreasing in grade, and tops out at 1.0 for executives.
func TestBonusFractionMonotonic(t *testing.T) {
	if BonusFractionForGrade(1) != 0.0 {
		t.Errorf("G1 bonus fraction = %.2f, want 0", BonusFractionForGrade(1))
	}
	if BonusFractionForGrade(10) != 1.0 {
		t.Errorf("G10 bonus fraction = %.2f, want 1.0", BonusFractionForGrade(10))
	}
	prev := -1.0
	for g := 1; g <= 10; g++ {
		f := BonusFractionForGrade(g)
		if f < prev {
			t.Errorf("bonus fraction not monotonic: G%d=%.2f < G%d=%.2f", g, f, g-1, prev)
		}
		prev = f
	}
}

// V1.19 — the profit gate: full payout through the 2010 peak, tapering
// in 2011-2012, zero from 2013 (and zero before the 1996 program start).
func TestBonusPayoutGate(t *testing.T) {
	cases := map[int]float64{
		1995: 0.0, 1996: 1.0, 2007: 1.0, 2010: 1.0,
		2011: 0.5, 2012: 0.25, 2013: 0.0, 2016: 0.0,
	}
	for y, want := range cases {
		if got := BonusPayoutFactorForYear(y); got != want {
			t.Errorf("BonusPayoutFactorForYear(%d) = %.2f, want %.2f", y, got, want)
		}
	}
}

// V1.19 — founders draw on a gentler ramp than ordinary corporate
// staff so they're never inverted below a manager in the lean years.
func TestFounderDrawAboveCorporate(t *testing.T) {
	for y := 1986; y < 2001; y++ {
		if FounderDrawFactor(y) <= CorporateMaturityFactor(y) {
			t.Errorf("year %d: FounderDrawFactor %.3f should exceed CorporateMaturityFactor %.3f",
				y, FounderDrawFactor(y), CorporateMaturityFactor(y))
		}
	}
	if FounderDrawFactor(2001) != 1.0 || FounderDrawFactor(2025) != 1.0 {
		t.Errorf("FounderDrawFactor should reach 1.0 by 2001, got %.3f / %.3f",
			FounderDrawFactor(2001), FounderDrawFactor(2025))
	}
	if !IsFounderRole(19) || !IsFounderRole(20) || IsFounderRole(3) {
		t.Errorf("IsFounderRole misclassified: 19=%v 20=%v 3=%v",
			IsFounderRole(19), IsFounderRole(20), IsFounderRole(3))
	}
}

// V1.20 — the used-mix curve must now drift smoothly (monotonic
// non-increasing, no clean multi-year plateaus) between the era anchors,
// instead of stepping (audit #6).
func TestNewVsUsedSplitSmooth(t *testing.T) {
	for _, c := range []struct {
		year int
		want float64
	}{{1986, 0.97}, {2007, 0.65}, {2015, 0.50}} {
		if got := NewVsUsedSplit(c.year); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("NewVsUsedSplit(%d)=%.4f, want anchor %.4f", c.year, got, c.want)
		}
	}
	// Strictly drifting across the old 0.65→0.50 plateau boundary, not flat.
	if !(NewVsUsedSplit(2008) < NewVsUsedSplit(2007) && NewVsUsedSplit(2008) > NewVsUsedSplit(2009)) {
		t.Errorf("expected smooth drift at 2007-2009, got %.4f/%.4f/%.4f",
			NewVsUsedSplit(2007), NewVsUsedSplit(2008), NewVsUsedSplit(2009))
	}
	prev := 1.1
	for y := 1986; y <= 2016; y++ {
		v := NewVsUsedSplit(y)
		if v > prev+1e-9 {
			t.Errorf("NewVsUsedSplit not monotonic at %d: %.4f > %.4f", y, v, prev)
		}
		prev = v
	}
}

// V1.20 — the per-shop daily volume must carry the price-rebalance
// scaling (so revenue/shop stays calibrated after the ~1.45× price lift),
// stay positive, and peak in the 2001-2010 boom.
func TestDailyTxRebalanced(t *testing.T) {
	peak := DailyTxPerShopMedian(2007)
	if peak <= 0 {
		t.Fatalf("DailyTxPerShopMedian(2007)=%.2f must be positive", peak)
	}
	// 78 raw × ~0.69 price-rebalance × ~0.53 hardware carve-out ≈ 29
	// (carve-out is tune-to-target, so the band is wide).
	if peak < 24 || peak > 40 {
		t.Errorf("DailyTxPerShopMedian(2007)=%.2f outside expected rebalanced+carveout band", peak)
	}
	if DailyTxPerShopMedian(2007) <= DailyTxPerShopMedian(2016) {
		t.Errorf("boom-era volume should exceed decline-era")
	}
	if BasePriceByCurrency["USD"] != 60.00 {
		t.Errorf("USD base price = %.2f, want 60.00", BasePriceByCurrency["USD"])
	}
}

// V1.21 — hardware launch-shape: zero before launch, peaks at years 0-1,
// then monotonically declines (the launch spike → tail).
func TestHardwareLaunchShape(t *testing.T) {
	if HardwareLaunchShape(-1) != 0 {
		t.Errorf("launch shape before launch = %.2f, want 0", HardwareLaunchShape(-1))
	}
	if HardwareLaunchShape(0) != 1.0 || HardwareLaunchShape(1) != 1.0 {
		t.Errorf("launch shape years 0-1 should peak at 1.0")
	}
	prev := HardwareLaunchShape(1)
	for y := 2; y <= 12; y++ {
		v := HardwareLaunchShape(y)
		if v > prev+1e-9 {
			t.Errorf("launch shape not monotone-decreasing at %d: %.2f > %.2f", y, v, prev)
		}
		prev = v
	}
}

// V1.21 — price-drop curve starts at full price and only declines.
func TestHardwarePriceDropCurve(t *testing.T) {
	if HardwarePriceDropCurve(0) != 1.0 {
		t.Errorf("price drop at year 0 = %.2f, want 1.0", HardwarePriceDropCurve(0))
	}
	prev := 1.0
	for y := 1; y <= 8; y++ {
		v := HardwarePriceDropCurve(y)
		if v > prev+1e-9 || v <= 0 {
			t.Errorf("price drop not monotone-decreasing/positive at %d: %.2f (prev %.2f)", y, v, prev)
		}
		prev = v
	}
}

// V1.21 — used-console share grows over time (new-share declines), and
// razor-and-blade COGS sits in the thin-margin band.
func TestHardwareSplitAndCOGS(t *testing.T) {
	prev := 1.01
	for _, y := range []int{1990, 2000, 2008, 2016} {
		v := HardwareNewVsUsedSplit(y)
		if v > prev+1e-9 || v <= 0 || v > 1 {
			t.Errorf("HardwareNewVsUsedSplit(%d)=%.2f not in a declining (0,1] band", y, v)
		}
		prev = v
	}
	for _, y := range []int{1990, 2007, 2016} {
		if c := HardwareCOGSRatio(y); c < 0.92 || c > 0.98 {
			t.Errorf("HardwareCOGSRatio(%d)=%.2f outside razor-and-blade band [0.92,0.98]", y, c)
		}
	}
}

// V1.22 — sales tax varies by country and steps over time.
func TestSalesTaxRate(t *testing.T) {
	// Per-country differentiation at a fixed year.
	us := SalesTaxRate("US", 2010)
	gb := SalesTaxRate("GB", 2010)
	se := SalesTaxRate("SE", 2010)
	if !(us < gb && gb < se) {
		t.Errorf("expected US(%.3f) < GB(%.3f) < SE(%.3f)", us, gb, se)
	}
	// Era steps.
	if !(SalesTaxRate("GB", 1985) < SalesTaxRate("GB", 2015)) {
		t.Errorf("GB VAT should rise over time: 1985=%.3f 2015=%.3f", SalesTaxRate("GB", 1985), SalesTaxRate("GB", 2015))
	}
	if SalesTaxRate("JP", 1985) != 0.0 {
		t.Errorf("JP consumption tax should be 0 before 1989, got %.3f", SalesTaxRate("JP", 1985))
	}
	if SalesTaxRate("AU", 1999) != 0.0 || SalesTaxRate("AU", 2001) != 0.10 {
		t.Errorf("AU GST should switch on in 2000: 1999=%.3f 2001=%.3f", SalesTaxRate("AU", 1999), SalesTaxRate("AU", 2001))
	}
	// Every country yields a sane rate.
	for _, c := range []string{"US", "GB", "DE", "FR", "IT", "ES", "NL", "JP", "CA", "AU", "BR", "KR", "CH", "SE", "NO", "DK", "PL", "CZ", "XX"} {
		r := SalesTaxRate(c, 2010)
		if r < 0 || r > 0.30 {
			t.Errorf("SalesTaxRate(%s,2010)=%.3f out of sane range", c, r)
		}
	}
}

// V1.22 — returns and promo gates are sane probabilities.
func TestReturnAndPromoGates(t *testing.T) {
	for _, y := range []int{1990, 2005, 2015} {
		if p := ReturnProbability(y); p < 0.03 || p > 0.10 {
			t.Errorf("ReturnProbability(%d)=%.3f outside ~5-8%% band", y, p)
		}
		if pf := PromoLineFraction(y); pf < 0 || pf > 0.20 {
			t.Errorf("PromoLineFraction(%d)=%.3f out of range", y, pf)
		}
	}
}

// V1.23.0 — canonical media must (a) cover every one of the 46 platforms
// with a value, (b) never emit the scraped-data howlers, (c) match the known
// physical format per platform, and (d) era-split DOS/Windows correctly.
func TestCanonicalMedia(t *testing.T) {
	// Exact dbo.platforms.name set (all 46). A "" return means a platform
	// slipped the switch — the exact bug that let scraped NULLs through.
	platforms := []string{
		"Apple II", "Atari 2600", "Intellivision", "DOS", "ColecoVision",
		"Commodore 64", "ZX Spectrum", "MSX", "Nintendo Entertainment System",
		"Commodore Amiga", "Sega Master System", "Atari 7800", "TurboGrafx-16",
		"Sega Genesis", "Atari Lynx", "Game Boy", "Game Gear", "Neo Geo",
		"Super Nintendo Entertainment System", "Philips CD-i", "Sega CD",
		"Microsoft Windows", "3DO Interactive Multiplayer", "Amiga CD32",
		"Atari Jaguar", "PlayStation", "Sega 32X", "Sega Saturn", "Virtual Boy",
		"Nintendo 64", "Game Boy Color", "Sega Dreamcast", "PlayStation 2",
		"Game Boy Advance", "Nintendo GameCube", "Xbox", "Nintendo DS",
		"PlayStation Portable", "Xbox 360", "Nintendo Wii", "PlayStation 3",
		"Nintendo 3DS", "PlayStation Vita", "Nintendo Wii U", "PlayStation 4",
		"Xbox One",
	}
	if len(platforms) != 46 {
		t.Fatalf("test platform list has %d entries, want 46", len(platforms))
	}
	// The scraped column was full of these; none may ever be emitted.
	howlers := map[string]bool{
		"": true, "digital download": true, "digital distribution": true,
		"video on demand": true, "vinyl record": true, "cloud computing": true,
		"theatrical release": true, "None": true, "optical disc": true,
	}
	for _, p := range platforms {
		m := CanonicalMedia(p, 2005)
		if howlers[m] {
			t.Errorf("CanonicalMedia(%q) returned howler/blank %q", p, m)
		}
	}
	// Spot-check the formats the user called out and the tricky ones.
	cases := []struct {
		platform string
		year     int
		want     string
	}{
		{"Nintendo 64", 2000, "Cartridge"},              // the Majora's Mask howler
		{"Super Nintendo Entertainment System", 1993, "Cartridge"},
		{"Sega Dreamcast", 1999, "GD-ROM"},
		{"PlayStation", 1996, "CD-ROM"},
		{"PlayStation 2", 2004, "DVD-ROM"},
		{"PlayStation 3", 2010, "Blu-ray Disc"},
		{"PlayStation Portable", 2006, "UMD"},
		{"Nintendo Wii", 2008, "Wii Optical Disc"},
		{"DOS", 1988, "Floppy Disk"},                    // pre-CD era
		{"DOS", 1997, "CD-ROM"},                         // post-CD era
		{"Microsoft Windows", 1998, "CD-ROM"},
		{"Microsoft Windows", 2010, "DVD-ROM"},
	}
	for _, c := range cases {
		if got := CanonicalMedia(c.platform, c.year); got != c.want {
			t.Errorf("CanonicalMedia(%q, %d) = %q, want %q", c.platform, c.year, got, c.want)
		}
	}
	// An unknown platform must return "" (→ NULL), not a guess.
	if got := CanonicalMedia("Sega Nomad", 1995); got != "" {
		t.Errorf("unknown platform should return \"\", got %q", got)
	}
}

// V1.23.0 — the hired-exec roles (21-24) must exist, sit outside the founder
// set (so they draw full exec comp, not the gentler founder draw), and carry
// executive pay grades.
func TestExecRoles(t *testing.T) {
	for _, id := range []int{21, 22, 23, 24} {
		r := RoleByID(id)
		if r.ID != id {
			t.Errorf("exec role %d missing from Roles", id)
			continue
		}
		if IsFounderRole(id) {
			t.Errorf("exec role %d must not be a founder role", id)
		}
	}
	// CEO/CFO are top grade; the webmaster emphatically is not.
	if RoleByID(21).DefaultGradeID != 10 || RoleByID(22).DefaultGradeID != 10 {
		t.Errorf("CEO/CFO should be grade 10")
	}
	if g := RoleByID(24).DefaultGradeID; g >= 8 {
		t.Errorf("webmaster grade %d should be well below the C-suite", g)
	}
}

// 2026-08 — the canon/non-canon tier split. The planted forensic anomalies
// (US-0009 fraud pocket + its emitFraudManager staff spell) are gated on this
// predicate and must be enabled ONLY on the lore-faithful large tiers. If this
// truth table ever changes, the fraud pocket and its manager spell can fall
// out of lockstep (a keyed refund referencing a spell that wasn't emitted).
func TestCanonAnomalies(t *testing.T) {
	cases := map[string]bool{
		"300g": true, "3t": true, // canon, lore-faithful
		"3g": false, "30g": false, // non-canon small tiers
		"": false, "8g": false, "banana": false, // unrecognised → non-canon
	}
	for tier, want := range cases {
		if got := CanonAnomalies(tier); got != want {
			t.Errorf("CanonAnomalies(%q) = %v, want %v", tier, got, want)
		}
	}
}
