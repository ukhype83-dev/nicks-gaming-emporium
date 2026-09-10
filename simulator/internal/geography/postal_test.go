package geography

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// V1.23.1 — era-aware proximity. Two same-population cities far apart;
// a shop opens near Nearville in 1987 and near Farton in 2005. A
// 1990-era draw must land almost entirely in Nearville; a 2010-era
// draw should split roughly evenly; SampleAnywhere ignores shops
// entirely. This is the fix for walk-in-era customers clustering
// around shops that didn't exist yet.
func TestSampleEraRespectsEraEstate(t *testing.T) {
	// Minimal postal TSV: header + one postcode per city. Columns per
	// Load(): 0=country 1=postal 2=city 3=region 4=admin1 6=admin2
	// 9=lat 10=lng (11 fields minimum).
	tsv := "country\tpostal\tcity\tregion\tadmin1\tx\tadmin2\tx\tx\tlat\tlng\n" +
		"US\t11111\tNearville\tTestState\tTS\t\tTest\t\t\t40.0000\t-80.0000\n" +
		"US\t22222\tFarton\tTestState\tTS\t\tTest\t\t\t45.0000\t-100.0000\n"
	path := filepath.Join(t.TempDir(), "postal_codes.tsv")
	if err := os.WriteFile(path, []byte(tsv), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	idx.ApplyPopulations(map[string][]CityPopEntry{
		"US|Nearville": {{Population: 100000, Latitude: 40.0, Longitude: -80.0}},
		"US|Farton":    {{Population: 100000, Latitude: 45.0, Longitude: -100.0}},
	})

	shops := []ShopLocation{
		{Country: "US", Latitude: 40.0, Longitude: -80.0, OpenedYear: 1987},  // Nearville
		{Country: "US", Latitude: 45.0, Longitude: -100.0, OpenedYear: 2005}, // Farton
	}
	idx.ApplyShopProximityEras(shops, 50.0, 0.05, []int{1990, 2000, 2010})

	count := func(year int, n int, seed uint64) (near, far int) {
		r := rand.New(rand.NewPCG(seed, seed))
		for i := 0; i < n; i++ {
			pc, ok := idx.SampleEra("US", year, r)
			if !ok {
				t.Fatalf("SampleEra returned no result (year %d)", year)
			}
			switch pc.City {
			case "Nearville":
				near++
			case "Farton":
				far++
			}
		}
		return near, far
	}

	// 1990 era: only the Nearville shop exists → Farton damped to 5%.
	// Expected Farton share ≈ 0.05/1.05 ≈ 4.8%.
	near, far := count(1990, 4000, 1)
	if farShare := float64(far) / float64(near+far); farShare > 0.12 {
		t.Errorf("1990 era: Farton share %.3f — walk-in customers are living near a shop that opens in 2005", farShare)
	}

	// 2010 era: both shops exist → both cities undamped → ~50/50.
	near, far = count(2010, 4000, 2)
	if share := float64(near) / float64(near+far); share < 0.40 || share > 0.60 {
		t.Errorf("2010 era: Nearville share %.3f, want ~0.5 (both cities in catchment)", share)
	}

	// SampleAnywhere: pure population, era-blind, ~50/50.
	r := rand.New(rand.NewPCG(3, 3))
	nearA, farA := 0, 0
	for i := 0; i < 4000; i++ {
		pc, ok := idx.SampleAnywhere("US", r)
		if !ok {
			t.Fatal("SampleAnywhere returned no result")
		}
		if pc.City == "Nearville" {
			nearA++
		} else if pc.City == "Farton" {
			farA++
		}
	}
	if share := float64(nearA) / float64(nearA+farA); share < 0.40 || share > 0.60 {
		t.Errorf("SampleAnywhere: Nearville share %.3f, want ~0.5 (population-weighted, shop-blind)", share)
	}

	// Legacy Sample() must see the FINAL era (all shops) → ~50/50 too.
	r = rand.New(rand.NewPCG(4, 4))
	nearL, farL := 0, 0
	for i := 0; i < 4000; i++ {
		pc, _ := idx.Sample("US", r)
		if pc.City == "Nearville" {
			nearL++
		} else if pc.City == "Farton" {
			farL++
		}
	}
	if share := float64(nearL) / float64(nearL+farL); share < 0.40 || share > 0.60 {
		t.Errorf("legacy Sample: Nearville share %.3f, want ~0.5 (final-era distribution)", share)
	}
}
