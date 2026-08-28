package catalog

import (
	_ "embed"
	"strconv"
	"strings"
	"time"
)

// Release-date corrections. A large slice of releases.tsv carries only a
// year-only "YYYY-01-01" fallback date (the upstream scrape knew the year but
// not the month/day). This table replaces those with the real date wherever we
// could recover one — from the row's own scraped free-text, its regional date
// columns, or Wikidata — so the transaction sampler (which gates a sale on the
// full ReleaseDate) and the review timeline sit in realistic time.
//
// Applied at load in Load(), immediately after the date is parsed and BEFORE
// the date sort / per-platform / per-(platform,year) indexes are built, so a
// correction flows through sampling, the stored first_release_date, and the
// web layer's ReleaseYear alike. Rows not present here keep their TSV date.
//
// The file is produced by build_release_date_overrides.py and is kept
// byte-identical here and in seed_data/ (this copy is the //go:embed target,
// mirroring genre_map.tsv). Columns: release_id, corrected_date (YYYY-MM-DD),
// source, old_date; the loader uses only the first two.
//
//go:embed release_date_overrides.tsv
var releaseDateOverridesTSV string

var releaseDateOverrides = buildReleaseDateOverrides()

func buildReleaseDateOverrides() map[int64]time.Time {
	m := make(map[int64]time.Time, 8192)
	for _, line := range strings.Split(releaseDateOverridesTSV, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil {
			continue // header row ("release_id") and any stray lines
		}
		d, err := time.Parse("2006-01-02", strings.TrimSpace(parts[1]))
		if err != nil {
			continue
		}
		m[id] = d
	}
	return m
}

// overrideReleaseDate returns the corrected release date for releaseID if one
// exists, otherwise the date parsed from the TSV unchanged.
func overrideReleaseDate(releaseID int64, parsed time.Time) time.Time {
	if d, ok := releaseDateOverrides[releaseID]; ok {
		return d
	}
	return parsed
}
