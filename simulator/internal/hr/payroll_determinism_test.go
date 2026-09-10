package hr

import (
	"testing"
	"time"

	"emporium/internal/shops"
)

// TestGeneratePayrollRunsDeterministic guards the map-iteration-order fix:
// payroll_run_id was assigned while ranging a Go map of countries, so the
// numbering differed on every run. With several payroll countries present,
// generating repeatedly must yield byte-identical runs (same ids, same order).
func TestGeneratePayrollRunsDeterministic(t *testing.T) {
	asOf := time.Date(2016, 9, 30, 0, 0, 0, 0, time.UTC)
	shopList := []shops.Shop{
		{CountryCode: "US", OpenedDate: "1986-08-06"},
		{CountryCode: "DE", OpenedDate: "1998-04-23"},
		{CountryCode: "GB", OpenedDate: "1991-01-01"},
		{CountryCode: "NO", OpenedDate: "2004-03-01"},
		{CountryCode: "DK", OpenedDate: "2006-09-01"},
		{CountryCode: "US", OpenedDate: "1990-01-01"},
	}
	base := GeneratePayrollRuns(42, asOf, shopList)
	if len(base) == 0 {
		t.Fatal("no payroll runs generated")
	}
	// Go randomizes map-iteration order per call, so a map-order dependency
	// surfaces within a handful of iterations.
	for i := 0; i < 25; i++ {
		got := GeneratePayrollRuns(42, asOf, shopList)
		if len(got) != len(base) {
			t.Fatalf("iter %d: %d runs vs %d", i, len(got), len(base))
		}
		for j := range base {
			if got[j] != base[j] {
				t.Fatalf("iter %d: run %d differs:\n base=%+v\n got =%+v", i, j, base[j], got[j])
			}
		}
	}
}
