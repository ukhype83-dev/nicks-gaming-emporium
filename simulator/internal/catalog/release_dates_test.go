package catalog

import (
	"testing"
	"time"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad test date %q: %v", s, err)
	}
	return d
}

// The embedded override table parses and a couple of known corrections apply.
// These ids are recovered locally (same-year refinement) and are stable in
// release_date_overrides.tsv.
func TestReleaseDateOverridesApply(t *testing.T) {
	if len(releaseDateOverrides) == 0 {
		t.Fatal("no overrides loaded — embed or parse failed")
	}

	cases := []struct {
		id       int64
		fallback string // the old YYYY-01-01 the TSV carried
		want     string // corrected date
	}{
		{23417, "2000-01-01", "2000-04-01"}, // Carmageddon — raw "April 1, 2000"
		{24075, "1998-01-01", "1998-11-30"}, // Centipede — raw "November 30, 1998"
		{20950, "1996-01-01", "1996-02-29"}, // Civilization II (Windows) — Wikidata P577 (matched via enwiki sitelink)
	}
	for _, c := range cases {
		got := overrideReleaseDate(c.id, mustDate(t, c.fallback))
		if !got.Equal(mustDate(t, c.want)) {
			t.Errorf("release %d: overrideReleaseDate = %s, want %s",
				c.id, got.Format("2006-01-02"), c.want)
		}
	}
}

// An id with no override passes the parsed date through unchanged.
func TestReleaseDateOverridePassthrough(t *testing.T) {
	const notOverridden = int64(-999999)
	in := mustDate(t, "1990-06-15")
	if got := overrideReleaseDate(notOverridden, in); !got.Equal(in) {
		t.Errorf("passthrough changed the date: got %s, want %s",
			got.Format("2006-01-02"), in.Format("2006-01-02"))
	}
}

// Every override must be a real, non-fallback date (never itself YYYY-01-01,
// which would carry no more information than what it replaced).
func TestReleaseDateOverridesAreNotFallbacks(t *testing.T) {
	for id, d := range releaseDateOverrides {
		if d.Month() == time.January && d.Day() == 1 {
			t.Errorf("release %d override is a Jan-1 fallback: %s", id, d.Format("2006-01-02"))
		}
	}
}
