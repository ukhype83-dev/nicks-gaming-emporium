// Package catalog loads the seed_data/releases.tsv export — the
// per-SKU information the transaction generator needs to decide which
// titles to put on each line.
//
// The source TSV is built by build_seed_catalog.py which joins
// dbo.releases with dbo.release_reception. One row per SKU.
//
// The Index sorts releases ascending by release date, so "what's
// available for sale on date X" is a prefix of the sorted slice
// (O(log N) binary search).
package catalog

import (
	"bufio"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Release is one row of seed_data/releases.tsv.
type Release struct {
	ReleaseID      int64
	Title          string
	Platform       string
	ReleaseDate    time.Time
	ReceptionScore float64
	LifetimeUnits  *int64
	// V1.14.2: passed-through fields for the dbo.releases table.
	// Generator logic ignores these — they exist only so the catalog
	// load can populate publisher / developer / genre / regional-date
	// columns in SQL Server instead of leaving them NULL. All sparse:
	// publisher ~96%, developer ~87%, media_type ~80%, regional dates
	// 33-38% pre-Wikidata enrichment (V1.14.3). "" means NULL.
	Publisher        string
	Developer        string
	Genre            string
	MediaType        string
	ReleaseJP        string // YYYY-MM-DD or ""
	ReleaseNA        string
	ReleaseEU        string
	ReleaseBR        string
	FirstReleaseRaw  string // free-text e.g. "November 20, 1995"
	SourceURL        string
	Notes            string
}

// Index holds all releases sorted by ReleaseDate ascending, plus a
// per-platform secondary index for lifecycle-aware sampling. Each
// platform's slice is also date-sorted (the byDate sort propagates
// because we append in iteration order).
//
// cumWeightByPlatformYear is the per-(platform, year) cumulative
// prefix-sum of age-decayed release weights. Each entry is parallel
// to byPlatform[name] in length and order. cum[i] is the sum of
// decayed_weight(rel[j]) for j ≤ i where rel[j].ReleaseDate.Year() ≤ year;
// releases not yet released by `year` contribute 0 (so cum[i] == cum[i-1]
// for those positions).
//
// SampleForPlatformWeighted does binary-search-by-date for the cutoff,
// then weighted random selection over cum[:cutoff]. PlatformWeightByYear
// just reads the last entry — total decayed weight at year-end.
//
// Memory: 46 platforms × ~45 years × ~1500 releases × 8 bytes ≈ 25 MB.
// Built once at catalog Load.
type Index struct {
	byDate                  []Release
	byPlatform              map[string][]Release
	cumWeightByPlatformYear map[platformYearKey][]float64
}

// platformYearKey identifies a (platform, year) cumulative-weight slice.
type platformYearKey struct {
	platform string
	year     int
}

// Year range pre-computed for cumWeightByPlatformYear. Covers NGE's
// founding (1986) through a few years past the current as-of, so
// future as-of bumps don't immediately need a recompute.
const (
	weightMinYear = 1986
	weightMaxYear = 2030
)

// ageDecay returns a multiplier in [0.05, 1.0] that weights recent
// releases more heavily. Single exponential with τ = 2.5 years and
// a 0.05 floor for the long tail.
//
// Without this multiplier, a 2003 PS1 transaction is equally likely
// to sample a 1995 release as a 2002 release, even though real-world
// sales heavily favour recent titles. With the curve, recent releases
// dominate while older catalogue still trades at the floor rate.
//
// Curve values (rounded):
//
//	age  0  →  1.00   (release year — peak)
//	age  1  →  0.67
//	age  2  →  0.45
//	age  3  →  0.30
//	age  5  →  0.14
//	age  7  →  0.06
//	age 10+ →  0.05   (floor — collector / retro)
func ageDecay(age int) float64 {
	if age < 0 {
		return 0
	}
	d := math.Exp(-float64(age) / 2.5)
	if d < 0.05 {
		return 0.05
	}
	return d
}

// computeReleaseWeight returns a sampling weight for a single release.
// Per §9.13.1 lifetime units is the primary anchor; reception_score is
// the fallback for releases without bestseller-derived units. Power-curve
// (^0.6) compresses the long tail so multi-million-unit titles dominate
// without erasing the back catalogue — empirically gives ~1% PS2-share
// for GTA San Andreas, matching its real-world ~1.1% of PS2 sales.
//
// Score → implied-units mapping for the fallback path:
//
//	score 65  →  100K  (default for releases with no reception data)
//	score 95  →  213K  (top non-bestseller, e.g. curated favourites)
//	score 35  →  ~29K  (curated dud)
//	score 1   →  ~24   (floor)
//
// The (^0.6) on units gives the final weight — bestsellers with millions
// of units land at ~10-100x the weight of average releases, which matches
// observed retail concentration.
func computeReleaseWeight(rel Release) float64 {
	var units float64
	if rel.LifetimeUnits != nil && *rel.LifetimeUnits > 0 {
		units = float64(*rel.LifetimeUnits)
	} else {
		score := rel.ReceptionScore
		if score < 1 {
			score = 1
		}
		ratio := score / 65.0
		units = 100_000 * ratio * ratio
	}
	return math.Pow(units, 0.6)
}

// Count returns total loaded releases.
func (idx *Index) Count() int { return len(idx.byDate) }

// All returns every loaded Release in sorted-by-date order. Returned
// slice is a view into the index — do not mutate.
func (idx *Index) All() []Release { return idx.byDate }

// Platforms returns the distinct platform names present in the index.
// Used to pre-compute lifecycle eligibility pools at sampler startup.
func (idx *Index) Platforms() []string {
	out := make([]string, 0, len(idx.byPlatform))
	for name := range idx.byPlatform {
		out = append(out, name)
	}
	return out
}

// SampleAvailable returns a uniformly-random release whose
// ReleaseDate ≤ on. Used by the transaction generator: a transaction
// on YYYY-MM-DD can only include titles released by that date.
//
// Returns (Release{}, false) if nothing is available by that date.
// Binary-searches the sorted slice for the cutoff, then picks a
// uniform random index in [0, cutoff).
func (idx *Index) SampleAvailable(on time.Time, r *rand.Rand) (Release, bool) {
	cutoff := sort.Search(len(idx.byDate), func(i int) bool {
		return idx.byDate[i].ReleaseDate.After(on)
	})
	if cutoff == 0 {
		return Release{}, false
	}
	return idx.byDate[r.IntN(cutoff)], true
}

// SampleForPlatform returns a uniformly-random release for the given
// platform whose ReleaseDate ≤ on. Retained for tests / debug; the
// transactions sampler uses SampleForPlatformWeighted in production.
func (idx *Index) SampleForPlatform(platform string, on time.Time, r *rand.Rand) (Release, bool) {
	rels, ok := idx.byPlatform[platform]
	if !ok || len(rels) == 0 {
		return Release{}, false
	}
	cutoff := sort.Search(len(rels), func(i int) bool {
		return rels[i].ReleaseDate.After(on)
	})
	if cutoff == 0 {
		return Release{}, false
	}
	return rels[r.IntN(cutoff)], true
}

// SampleForPlatformWeighted draws proportionally to age-decayed
// release weight at the transaction year. Recent releases dominate;
// the back catalogue still trades at the 0.05 floor rate set in
// ageDecay. A bestseller in its release year is sampled ~10-100× more
// often than an average back-catalogue release on the same platform.
//
// Two-step:
//  1. Binary-search the date-sorted per-platform slice for the cutoff
//     index where ReleaseDate > on.
//  2. Sample a uniform target ∈ [0, cum[cutoff-1]); binary-search
//     the year-specific cumulative prefix-sum for the first index
//     whose cumulative weight exceeds target.
//
// Year is derived from `on`. Falls back to uniform sampling if the
// year is outside the precomputed range or all weights are zero.
func (idx *Index) SampleForPlatformWeighted(platform string, on time.Time, r *rand.Rand) (Release, bool) {
	rels, ok := idx.byPlatform[platform]
	if !ok || len(rels) == 0 {
		return Release{}, false
	}
	cutoff := sort.Search(len(rels), func(i int) bool {
		return rels[i].ReleaseDate.After(on)
	})
	if cutoff == 0 {
		return Release{}, false
	}
	cum, ok := idx.cumWeightByPlatformYear[platformYearKey{platform, on.Year()}]
	if !ok {
		// Year outside precomputed range — uniform fallback.
		return rels[r.IntN(cutoff)], true
	}
	totalWeight := cum[cutoff-1]
	if totalWeight <= 0 {
		return rels[r.IntN(cutoff)], true
	}
	target := r.Float64() * totalWeight
	pick := sort.SearchFloat64s(cum[:cutoff], target)
	if pick >= cutoff {
		pick = cutoff - 1
	}
	return rels[pick], true
}

// PlatformWeightByYear returns the total age-decayed sampling weight
// of `platform`'s releases as of `year`. Used by the transactions
// sampler to weight platform-pick proportionally to platform catalogue
// activity AT THE TRANSACTION YEAR — so PS2 in 2003 (active hits at
// peak velocity) carries more weight than PS1 in 2003 (huge catalogue
// but mostly decayed back to the 0.05 floor), matching real-world
// sales mix far better than the V1.3.0 lifetime-weight model.
//
// Returns 0 for unknown platforms, years outside the precomputed
// range, or platforms with nothing released by year-end.
func (idx *Index) PlatformWeightByYear(platform string, year int) float64 {
	cum, ok := idx.cumWeightByPlatformYear[platformYearKey{platform, year}]
	if !ok || len(cum) == 0 {
		return 0
	}
	return cum[len(cum)-1]
}

// Load parses seed_data/releases.tsv into an Index.
func Load(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open releases tsv: %w", err)
	}
	defer f.Close()

	idx := &Index{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo == 1 {
			continue // header
		}
		line := scanner.Text()
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			continue
		}
		releaseID, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		releaseDate, err := time.Parse("2006-01-02", fields[3])
		if err != nil {
			continue
		}
		score, err := strconv.ParseFloat(fields[4], 64)
		if err != nil {
			score = 65.0
		}
		var units *int64
		if len(fields) >= 6 && fields[5] != "" {
			if u, err := strconv.ParseInt(fields[5], 10, 64); err == nil {
				units = &u
			}
		}
		// V1.14.2 schema: cols 6-15 are publisher, developer, genre,
		// media_type, release_jp, release_na, release_eu, release_br,
		// first_release_raw, source_url, notes. Sparse strings — "" is OK.
		// Pre-V1.14.2 TSVs only had 6 columns; we accept those for backwards
		// compatibility (the new fields stay empty → SQL NULL downstream).
		fieldAt := func(i int) string {
			if i < len(fields) {
				return fields[i]
			}
			return ""
		}
		idx.byDate = append(idx.byDate, Release{
			ReleaseID:       releaseID,
			Title:           fields[1],
			Platform:        fields[2],
			ReleaseDate:     releaseDate,
			ReceptionScore:  score,
			LifetimeUnits:   units,
			Publisher:       fieldAt(6),
			Developer:       fieldAt(7),
			Genre:           fieldAt(8),
			MediaType:       fieldAt(9),
			ReleaseJP:       fieldAt(10),
			ReleaseNA:       fieldAt(11),
			ReleaseEU:       fieldAt(12),
			ReleaseBR:       fieldAt(13),
			FirstReleaseRaw: fieldAt(14),
			SourceURL:       fieldAt(15),
			Notes:           fieldAt(16),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read releases tsv: %w", err)
	}
	sort.Slice(idx.byDate, func(i, j int) bool {
		return idx.byDate[i].ReleaseDate.Before(idx.byDate[j].ReleaseDate)
	})
	// Build per-platform secondary index. Iteration in date order
	// preserves date-sortedness within each per-platform slice.
	idx.byPlatform = make(map[string][]Release, 64)
	for _, rel := range idx.byDate {
		idx.byPlatform[rel.Platform] = append(idx.byPlatform[rel.Platform], rel)
	}
	// Build per-(platform, year) cumulative age-decayed weight prefix
	// sums. For each year in the precomputed range, walk the platform's
	// date-sorted releases, decay each release's lifetime weight by its
	// age relative to `year`, accumulate. Releases not yet released by
	// `year` contribute 0 (cum[i] == cum[i-1] for those positions),
	// which keeps the slice index aligned with byPlatform[name].
	expectedKeys := len(idx.byPlatform) * (weightMaxYear - weightMinYear + 1)
	idx.cumWeightByPlatformYear = make(map[platformYearKey][]float64, expectedKeys)
	for name, rels := range idx.byPlatform {
		baseWeights := make([]float64, len(rels))
		for i, rel := range rels {
			baseWeights[i] = computeReleaseWeight(rel)
		}
		for year := weightMinYear; year <= weightMaxYear; year++ {
			cum := make([]float64, len(rels))
			var sum float64
			for i, rel := range rels {
				relYear := rel.ReleaseDate.Year()
				if relYear <= year {
					sum += baseWeights[i] * ageDecay(year-relYear)
				}
				cum[i] = sum
			}
			idx.cumWeightByPlatformYear[platformYearKey{name, year}] = cum
		}
	}
	return idx, nil
}
