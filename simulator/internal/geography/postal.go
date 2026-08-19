// Package geography loads the GeoNames postal-code seed data and
// provides deterministic samplers + locale-aware street-address
// generation for shop and customer addresses.
//
// Postal codes come from seed_data/postal_codes.tsv (built by
// build_seed_postal_codes.py). City populations come from
// seed_data/city_populations.tsv (built by
// build_seed_city_populations.py from GeoNames cities1000). Both
// licensed CC BY 4.0 from GeoNames — attribution preserved in
// LICENSE.md / seed_data/LICENSES.md.
//
// V1.11: sampling is population-weighted at city granularity. Each
// postal_codes.tsv place_name is matched to a city_populations.tsv
// row by (country, place_name) — the city's population sets the
// chance of being picked. Within a chosen city, postal codes are
// sampled uniformly. Place names without a population match get
// `unknownCityWeight` as a floor so they're still occasionally
// reachable, just not at parity with major cities.
package geography

import (
	"bufio"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"
)

// unknownCityWeight is the population assigned to a postal-code
// place_name that has no match in city_populations.tsv. Roughly the
// median population of a sub-500-population rural locale — small
// enough that Manchester (568K) gets ~5000× more weight, large enough
// that rural codes aren't completely starved.
const unknownCityWeight = 100

// PostalCode is one row of seed_data/postal_codes.tsv.
//
// AdminCode1 / AdminCode2 added V1.13.2 (kept in V1.13.3): place_name
// alone is not a unique key in GeoNames. Several UK villages share the
// name "Chorley" (Lancashire, Cheshire, Shropshire, Staffordshire);
// pre-V1.13.2 the sampler grouped them all under one bucket and mixed
// their postcodes (so a "Chorley, England, WV16" customer might appear,
// where WV16 is actually Bridgnorth/Shropshire's prefix, not
// Chorley/Lancashire's). admin1+admin2 are used to keep the buckets
// distinct; population is then assigned per-bucket via lat/long
// proximity matching (see ApplyPopulations) since the admin2 code
// formats diverge between postal_codes.tsv and cities1000.
type PostalCode struct {
	Country    string
	Postal     string
	City       string  // place_name
	Region     string  // admin_name1 (UK country / US state / etc.)
	AdminCode1 string  // GeoNames admin_code1
	AdminCode2 string  // GeoNames admin_code2 (county/district level)
	Latitude   float64
	Longitude  float64
}

// cityBucket groups all postal codes that share the same (country,
// place_name) and carries the city's population as the sampling weight.
type cityBucket struct {
	name       string
	population int
	postals    []PostalCode
}

// ShopLocation is the minimal information ApplyShopProximity needs
// from each shop — its country and lat/lng. Build the slice from
// `[]shops.Shop` at the caller and pass it in. Keeps this package
// import-cycle-free (it doesn't need to depend on shops).
//
// V1.23.1: OpenedYear feeds ApplyShopProximityEras so era-aware
// callers can damp against the estate that existed at a customer's
// signup date, not the lifetime footprint. Callers that only use
// ApplyShopProximity may leave it zero.
type ShopLocation struct {
	Country    string
	Latitude   float64
	Longitude  float64
	OpenedYear int
}

// Index supports three sampling modes:
//
//   - V1.x  uniform per-postcode (Load only)
//   - V1.11 population-weighted per-city (ApplyPopulations)
//   - V1.13 shop-proximity-damped per-city (ApplyShopProximity)
//
// Sample() uses whichever is most recent. SampleAnywhere() always
// uses the pre-proximity (V1.11) distribution if it was ever built,
// so era-aware callers can opt out of the proximity damping for
// online-era customers who could plausibly live anywhere in their
// country.
type Index struct {
	byCountry map[string][]PostalCode

	// Set by ApplyPopulations. nil means uniform sampling is in effect.
	cities    map[string][]cityBucket
	cumWeight map[string][]int // parallel to cities; cumulative sum for binary search

	// Snapshot of cumWeight taken at the moment ApplyShopProximity is
	// called — preserves the pre-damping (pure population-weighted)
	// distribution for SampleAnywhere(). nil if ApplyShopProximity was
	// never called.
	cumWeightAnywhere map[string][]int

	// V1.23.1 — per-era damped distributions (ApplyShopProximityEras).
	// eraBounds is ascending; cumWeightByEra[country][e] is the
	// cumulative-weight array damped against shops with
	// OpenedYear <= eraBounds[e]. nil if the era pass was never run.
	eraBounds      []int
	cumWeightByEra map[string][][]int
}

// Countries returns the set of country codes with loaded postal data.
func (idx *Index) Countries() []string {
	out := make([]string, 0, len(idx.byCountry))
	for c := range idx.byCountry {
		out = append(out, c)
	}
	return out
}

// CountFor returns the number of loaded postal codes for a country.
func (idx *Index) CountFor(country string) int {
	return len(idx.byCountry[country])
}

// Sample returns a postal code for the given country, weighted by
// city population if ApplyPopulations was called, else uniform.
// Deterministic given the RNG state.
//
// Ordering guarantee under uniform: the per-country slice is in the
// same order the TSV file delivered it (alphabetical by postal code).
// Under weighted: cities are sorted by (population desc, name asc) so
// the cumulative-weight array is deterministic across runs.
func (idx *Index) Sample(country string, r *rand.Rand) (PostalCode, bool) {
	if idx.cities != nil {
		buckets, ok := idx.cities[country]
		if !ok || len(buckets) == 0 {
			return PostalCode{}, false
		}
		cum := idx.cumWeight[country]
		total := cum[len(cum)-1]
		pick := r.IntN(total)
		// Binary search for the smallest i where cum[i] > pick.
		lo, hi := 0, len(cum)-1
		for lo < hi {
			mid := (lo + hi) / 2
			if cum[mid] > pick {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		bucket := buckets[lo]
		return bucket.postals[r.IntN(len(bucket.postals))], true
	}
	rows, ok := idx.byCountry[country]
	if !ok || len(rows) == 0 {
		return PostalCode{}, false
	}
	return rows[r.IntN(len(rows))], true
}

// SampleByCity returns a uniformly-random postal code whose place_name
// matches `city` (exact string match against the postal-side spelling).
// If `region` is non-empty, also filters by `admin_code1` so e.g.
// ("Atlanta", "GA") doesn't accidentally land on Atlanta, Idaho.
// Returns (PostalCode{}, false) if no match.
//
// V1.13.6: used by shops.Generate to honour the per-country
// AnchorCities list — major metros that should reliably get at least
// one shop rather than relying on population-weighted sampling to land
// on them. V1.13.7: added region parameter after Atlanta and Boston
// anchors landed in Atlanta ID / Boston IN.
func (idx *Index) SampleByCity(country, city, region string, r *rand.Rand) (PostalCode, bool) {
	rows := idx.byCountry[country]
	var matches []PostalCode
	for _, pc := range rows {
		if pc.City != city {
			continue
		}
		if region != "" && pc.AdminCode1 != region {
			continue
		}
		matches = append(matches, pc)
	}
	if len(matches) == 0 {
		return PostalCode{}, false
	}
	return matches[r.IntN(len(matches))], true
}

// SampleByCityRegionName is SampleByCity for callers holding a full
// region NAME ("Texas") rather than an admin code ("TX") — e.g. anyone
// matching against a shop address, whose Region field carries the full
// name. V1.24.0 (added for the fraud-pocket manager, who should live
// in his shop's city, not a random one).
func (idx *Index) SampleByCityRegionName(country, city, regionName string, r *rand.Rand) (PostalCode, bool) {
	rows := idx.byCountry[country]
	var matches []PostalCode
	for _, pc := range rows {
		if pc.City != city {
			continue
		}
		if regionName != "" && pc.Region != regionName {
			continue
		}
		matches = append(matches, pc)
	}
	if len(matches) == 0 {
		return PostalCode{}, false
	}
	return matches[r.IntN(len(matches))], true
}

// SampleAnywhere returns a postal code drawn from the pure
// population-weighted distribution (pre-ApplyShopProximity). Use this
// for callers that want a customer who can plausibly live anywhere in
// the country, regardless of shop proximity — e.g. online-era
// customers whose physical location is decoupled from the store
// network.
//
// If ApplyShopProximity has not been called, this is equivalent to
// Sample(). If neither ApplyPopulations nor proximity has been called,
// falls back to uniform per-postcode sampling.
func (idx *Index) SampleAnywhere(country string, r *rand.Rand) (PostalCode, bool) {
	if idx.cumWeightAnywhere != nil && idx.cities != nil {
		buckets, ok := idx.cities[country]
		if !ok || len(buckets) == 0 {
			return PostalCode{}, false
		}
		cum, ok := idx.cumWeightAnywhere[country]
		if !ok || len(cum) == 0 {
			return PostalCode{}, false
		}
		total := cum[len(cum)-1]
		pick := r.IntN(total)
		lo, hi := 0, len(cum)-1
		for lo < hi {
			mid := (lo + hi) / 2
			if cum[mid] > pick {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		bucket := buckets[lo]
		return bucket.postals[r.IntN(len(bucket.postals))], true
	}
	// No proximity applied → primary distribution IS the anywhere
	// distribution. Fall through to Sample.
	return idx.Sample(country, r)
}

// CityPopEntry is one cities1000 row used for population-weighted
// sampling. Lat/long disambiguate same-named places at lookup time
// (V1.13.3) — see ApplyPopulations.
type CityPopEntry struct {
	Population int
	Latitude   float64
	Longitude  float64
}

// collisionRadiusKm is the maximum distance (great-circle) between a
// postal-code bucket centroid and a cities1000 candidate of the same
// name before they're considered the same place. Beyond this, the
// candidate is ignored and the bucket falls to unknownCityWeight.
//
// 30 km comfortably separates the four UK Chorleys (Lancashire,
// Cheshire 67 km, Staffordshire 133 km, Shropshire 127 km) and the
// dozens of US Springfields (typically 100s of km apart) while still
// tolerating minor centroid drift between the two GeoNames sources.
const collisionRadiusKm = 30.0

// LoadCityPopulations reads seed_data/city_populations.tsv and
// returns a map keyed by "COUNTRY|name" → list of (pop, lat, lng)
// candidates. Used by ApplyPopulations to weight per-city sampling.
//
// V1.13.3: collisions on place_name are disambiguated by lat/long
// proximity rather than admin codes (admin2 formats diverge between
// the GeoNames postal-code export and the cities1000/500 dumps).
//
// V1.13.4: source bumped from cities1000 → cities500 (population
// threshold ≥500 instead of ≥1000) for ~35% more long-tail coverage.
// Both unicode `name` and `ascii_name` keys are inserted so
// postal-codes-side mojibake or transliteration variants still match.
//
// V1.13.5: alias rules close the long-standing spelling gaps between
// postal_codes.tsv and cities500 — most notably "New York"
// (postal/Manhattan) vs "Manhattan" (cities500), "Bronx" (postal) vs
// "The Bronx" (cities500), and the ~8 "St. X" entries that postal
// data spells "Saint X". Without these, Manhattan/Bronx/St-something
// postcodes fall to unknownCityWeight despite being among the most
// populous US cities. See `cityAliasesFor`.
func LoadCityPopulations(path string) (map[string][]CityPopEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open city populations: %w", err)
	}
	defer f.Close()
	pops := make(map[string][]CityPopEntry)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo == 1 {
			continue // skip header
		}
		fields := strings.Split(scanner.Text(), "\t")
		// V1.13.3 schema: country_code, name, ascii_name, admin_code1,
		// admin_code2, population, latitude, longitude.
		if len(fields) < 8 {
			continue
		}
		country := fields[0]
		name := fields[1]
		asciiName := fields[2]
		admin1 := fields[3]
		pop, _ := strconv.Atoi(fields[5])
		if pop <= 0 {
			continue
		}
		lat, _ := strconv.ParseFloat(fields[6], 64)
		lng, _ := strconv.ParseFloat(fields[7], 64)
		entry := CityPopEntry{Population: pop, Latitude: lat, Longitude: lng}
		insert := func(n string) {
			pops[country+"|"+n] = append(pops[country+"|"+n], entry)
		}
		insert(name)
		if asciiName != "" && asciiName != name {
			insert(asciiName)
		}
		for _, alias := range cityAliasesFor(country, admin1, name) {
			insert(alias)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read city populations: %w", err)
	}
	return pops, nil
}

// cityAliasesFor returns the postal-side spellings that a given
// cities500 row should also be discoverable under. Keep this list tight
// — every alias risks pulling a postal name towards a same-named but
// genuinely different cities500 candidate (the greedy lat/long matcher
// in ApplyPopulations is the second line of defence; if two cities500
// rows are <30 km apart it picks the closer one, but unrelated rows
// further away would still steal the alias if their name matches
// exactly).
func cityAliasesFor(country, admin1, name string) []string {
	var out []string
	switch country {
	case "US":
		// "Saint X" postal spelling → cities500 "St. X". Affects St. Louis MO,
		// St. Petersburg FL, St. Johns FL, St. Charles MD/IL, St. Marys GA,
		// etc. (~8 cities500 rows).
		if strings.HasPrefix(name, "St. ") {
			out = append(out, "Saint "+name[len("St. "):])
		}
		// NYC Manhattan postcodes (ZIPs 100xx-102xx) are tagged "New York"
		// in postal_codes.tsv, but the matching cities500 entry is
		// "Manhattan" (or "New York City" for the whole-city aggregate).
		// Manhattan's borough population (1.5M) is the right weight to
		// inherit — using "New York City" 8.8M would double-count since
		// Brooklyn / Queens / The Bronx / Staten Island already match
		// separately by their own names. Scoped to admin1=NY so we don't
		// catch Manhattan KS / IL / MT.
		if name == "Manhattan" && admin1 == "NY" {
			out = append(out, "New York")
		}
		// Bronx postcodes are tagged "Bronx" (10451-10475) — cities500's
		// entry is "The Bronx". 1.4M population.
		if name == "The Bronx" {
			out = append(out, "Bronx")
		}
	}
	return out
}

// ApplyPopulations enables population-weighted sampling on the index.
//
// V1.13.4: groups postal codes by (place_name, admin_code1,
// admin_code2) so distinct same-named places (four UK Chorleys, dozens
// of US Springfields, etc.) become separate buckets. Each bucket's
// centroid (average of its postal-code lat/longs) is then matched to
// a cities500 candidate of the same name via **greedy bipartite
// matching**: among all (candidate, bucket) edges within
// `collisionRadiusKm`, the shortest-distance edge is assigned first
// and both endpoints are then locked. This guarantees that each
// candidate's population goes to exactly one bucket (its closest)
// rather than being claimed by every bucket within the radius —
// which had Sutton-near-Dorking inheriting London-Borough Sutton's
// 187k weight in V1.13.3 just because it was 22 km away.
//
// Buckets that don't win any candidate within the radius fall to
// `unknownCityWeight` so they're still occasionally sampled but
// don't double-count a real city's population.
//
// Note that all buckets sharing a place_name still surface under the
// same `City` display value — querying "customers in Chorley" naturally
// aggregates all four real Chorleys. The disambiguation is internal:
// within a single customer's address fields, postal_code / lat / lng /
// admin codes are all geographically consistent.
func (idx *Index) ApplyPopulations(pops map[string][]CityPopEntry) {
	idx.cities = make(map[string][]cityBucket, len(idx.byCountry))
	idx.cumWeight = make(map[string][]int, len(idx.byCountry))
	for country, rows := range idx.byCountry {
		// Group by composite key preserving first-seen order.
		grouped := make(map[string]*cityBucket)
		groupLats := make(map[string]float64)
		groupLngs := make(map[string]float64)
		groupCounts := make(map[string]int)
		order := []string{}
		for _, row := range rows {
			key := row.City + "|" + row.AdminCode1 + "|" + row.AdminCode2
			b, ok := grouped[key]
			if !ok {
				b = &cityBucket{name: row.City}
				grouped[key] = b
				order = append(order, key)
			}
			b.postals = append(b.postals, row)
			groupLats[key] += row.Latitude
			groupLngs[key] += row.Longitude
			groupCounts[key]++
		}
		// Group bucket keys by name so we can run the bipartite match
		// per (country, name) group.
		bucketsByName := make(map[string][]string)
		nameOrder := []string{}
		seenName := make(map[string]bool)
		for _, key := range order {
			name := grouped[key].name
			if !seenName[name] {
				nameOrder = append(nameOrder, name)
				seenName[name] = true
			}
			bucketsByName[name] = append(bucketsByName[name], key)
		}
		// Greedy bipartite matching per name group.
		for _, name := range nameOrder {
			keys := bucketsByName[name]
			cands := pops[country+"|"+name]
			if len(cands) == 0 {
				continue
			}
			type edge struct {
				cand int
				key  string
				dist float64
			}
			var edges []edge
			for ci, c := range cands {
				for _, key := range keys {
					n := float64(groupCounts[key])
					clat := groupLats[key] / n
					clng := groupLngs[key] / n
					d := haversineKm(clat, clng, c.Latitude, c.Longitude)
					if d <= collisionRadiusKm {
						edges = append(edges, edge{cand: ci, key: key, dist: d})
					}
				}
			}
			if len(edges) == 0 {
				continue
			}
			// Sort by distance asc; tiebreak by (candIdx, keyOrder) for
			// determinism — cities500 rows for one name are TSV-ordered
			// and `keys` preserves first-seen postal order.
			keyRank := make(map[string]int, len(keys))
			for i, k := range keys {
				keyRank[k] = i
			}
			sort.SliceStable(edges, func(i, j int) bool {
				if edges[i].dist != edges[j].dist {
					return edges[i].dist < edges[j].dist
				}
				if edges[i].cand != edges[j].cand {
					return edges[i].cand < edges[j].cand
				}
				return keyRank[edges[i].key] < keyRank[edges[j].key]
			})
			candTaken := make(map[int]bool)
			keyTaken := make(map[string]bool)
			for _, e := range edges {
				if candTaken[e.cand] || keyTaken[e.key] {
					continue
				}
				grouped[e.key].population = cands[e.cand].Population
				candTaken[e.cand] = true
				keyTaken[e.key] = true
			}
		}
		// Any bucket still at zero gets the unknown-city floor.
		for _, key := range order {
			if grouped[key].population == 0 {
				grouped[key].population = unknownCityWeight
			}
		}
		// Materialise as a slice ordered by (population desc, name asc).
		buckets := make([]cityBucket, 0, len(order))
		for _, key := range order {
			buckets = append(buckets, *grouped[key])
		}
		sortBuckets(buckets)
		cum := make([]int, len(buckets))
		running := 0
		for i, b := range buckets {
			running += b.population
			cum[i] = running
		}
		idx.cities[country] = buckets
		idx.cumWeight[country] = cum
	}
}

// ApplyShopProximity damps the sampling weight of cities that aren't
// within `radiusKm` of any shop in the same country. Cities inside the
// catchment keep their full ApplyPopulations weight; cities outside
// get multiplied by `farFactor` (e.g. 0.05 → "1-in-20 chance compared
// to a same-sized city near a shop"). Must be called AFTER
// ApplyPopulations so the bucket structure exists.
//
// Mechanism: each city's centroid (taken from the first postal code's
// lat/lng) is compared to the nearest shop's lat/lng via haversine.
// Cumulative weights are rebuilt at the end so Sample() picks up the
// new distribution.
//
// Countries with no shops are left untouched — the per-country
// customer count is driven by ShopShares elsewhere, so zero-shop
// countries already produce zero customers.
//
// Rationale: customer addresses pre-V1.13 were population-weighted
// nationally, so a 1986-2025 retailer with shops in five UK cities
// still had customers spread across every Welsh hamlet. Real chains
// have customer catchments around their stores. 50 km is the typical
// "I'll drive to this shop" radius; far-factor 0.05 leaves room for
// mail-order / online customers (post-1999) outside catchments
// without making them numerous.
func (idx *Index) ApplyShopProximity(shopLocs []ShopLocation, radiusKm float64, farFactor float64) {
	if idx.cities == nil {
		return // no population data; nothing to weight
	}
	// Snapshot the pre-damping cumulative weights so SampleAnywhere can
	// still draw from the pure population-weighted distribution. This
	// supports era-aware callers: online-era customers shouldn't be
	// pinned to shop catchments since they're shopping from home.
	idx.cumWeightAnywhere = make(map[string][]int, len(idx.cumWeight))
	for country, cum := range idx.cumWeight {
		clone := make([]int, len(cum))
		copy(clone, cum)
		idx.cumWeightAnywhere[country] = clone
	}
	// Group shops by country.
	shopsByCountry := make(map[string][]ShopLocation)
	for _, s := range shopLocs {
		shopsByCountry[s.Country] = append(shopsByCountry[s.Country], s)
	}
	radiusSq := radiusKm * radiusKm
	for country, buckets := range idx.cities {
		countryShops := shopsByCountry[country]
		if len(countryShops) == 0 {
			continue
		}
		for i := range buckets {
			if len(buckets[i].postals) == 0 {
				continue
			}
			lat0 := buckets[i].postals[0].Latitude
			lng0 := buckets[i].postals[0].Longitude
			// Find squared-distance to nearest shop (squared to skip the
			// sqrt for cities that are obviously near). Switch to actual
			// haversine if no near-enough shop is found by the cheap path.
			nearest := math.Inf(1)
			for _, sh := range countryShops {
				d := haversineKm(lat0, lng0, sh.Latitude, sh.Longitude)
				if d < nearest {
					nearest = d
					if nearest <= radiusKm {
						break
					}
				}
			}
			_ = radiusSq // unused — kept above for future Euclidean-prefilter optimization
			if nearest > radiusKm {
				newPop := int(float64(buckets[i].population) * farFactor)
				if newPop < 1 {
					newPop = 1
				}
				buckets[i].population = newPop
			}
		}
		// Rebuild cumulative weights for this country.
		cum := make([]int, len(buckets))
		running := 0
		for i, b := range buckets {
			running += b.population
			cum[i] = running
		}
		idx.cumWeight[country] = cum
	}
}

// ApplyShopProximityEras is the V1.23.1 era-aware version of
// ApplyShopProximity. The lifetime-estate damping above let a 1991
// paper-loyalty customer live beside a shop that wouldn't open until
// 2007 (at large tiers ~every populous city is within 50 km of SOME
// eventual shop, so the damping degenerated to population sampling for
// every era). This variant computes one damped distribution per era
// bucket, counting only shops with OpenedYear <= that era's bound, so
// SampleEra can place walk-in-era customers near the estate that
// actually existed when they signed up.
//
// Contract matches ApplyShopProximity: call after ApplyPopulations;
// snapshots the pure population distribution for SampleAnywhere; and
// sets cumWeight to the FINAL era's (all-shops) distribution so legacy
// Sample() callers behave exactly as before. Does not mutate bucket
// populations. eraBounds must be ascending and end at or beyond the
// last shop-opening year.
func (idx *Index) ApplyShopProximityEras(shopLocs []ShopLocation, radiusKm, farFactor float64, eraBounds []int) {
	if idx.cities == nil || len(eraBounds) == 0 {
		return
	}
	idx.cumWeightAnywhere = make(map[string][]int, len(idx.cumWeight))
	for country, cum := range idx.cumWeight {
		clone := make([]int, len(cum))
		copy(clone, cum)
		idx.cumWeightAnywhere[country] = clone
	}
	shopsByCountry := make(map[string][]ShopLocation)
	for _, s := range shopLocs {
		shopsByCountry[s.Country] = append(shopsByCountry[s.Country], s)
	}
	idx.eraBounds = append([]int(nil), eraBounds...)
	idx.cumWeightByEra = make(map[string][][]int, len(idx.cities))
	for country, buckets := range idx.cities {
		countryShops := shopsByCountry[country]
		perEra := make([][]int, len(eraBounds))
		for e, bound := range eraBounds {
			eraShops := make([]ShopLocation, 0, len(countryShops))
			for _, sh := range countryShops {
				if sh.OpenedYear <= bound {
					eraShops = append(eraShops, sh)
				}
			}
			cum := make([]int, len(buckets))
			running := 0
			for i := range buckets {
				w := buckets[i].population
				// Countries/eras with no shops yet keep pure population
				// weights — same semantics as ApplyShopProximity for
				// zero-shop countries.
				if len(buckets[i].postals) > 0 && len(eraShops) > 0 {
					lat0 := buckets[i].postals[0].Latitude
					lng0 := buckets[i].postals[0].Longitude
					nearest := math.Inf(1)
					for _, sh := range eraShops {
						d := haversineKm(lat0, lng0, sh.Latitude, sh.Longitude)
						if d < nearest {
							nearest = d
							if nearest <= radiusKm {
								break
							}
						}
					}
					if nearest > radiusKm {
						w = int(float64(w) * farFactor)
						if w < 1 {
							w = 1
						}
					}
				}
				running += w
				cum[i] = running
			}
			perEra[e] = cum
		}
		idx.cumWeightByEra[country] = perEra
		idx.cumWeight[country] = perEra[len(perEra)-1]
	}
}

// SampleEra draws like Sample() but against the shop estate as of a
// given year: the era bucket is the largest bound <= year, clamped to
// the first bucket for pre-bound years (a slight forward smear over
// the founding handful of shops) and the last for later years. Lag,
// not lead: a customer between bounds samples against slightly FEWER
// shops than truly existed, never against future ones. Falls back to
// Sample() if ApplyShopProximityEras was never called.
func (idx *Index) SampleEra(country string, year int, r *rand.Rand) (PostalCode, bool) {
	if idx.cumWeightByEra == nil || idx.cities == nil {
		return idx.Sample(country, r)
	}
	perEra, ok := idx.cumWeightByEra[country]
	buckets := idx.cities[country]
	if !ok || len(perEra) == 0 || len(buckets) == 0 {
		return idx.Sample(country, r)
	}
	e := 0
	for i, bound := range idx.eraBounds {
		if year >= bound {
			e = i
		}
	}
	cum := perEra[e]
	total := cum[len(cum)-1]
	if total <= 0 {
		return idx.Sample(country, r)
	}
	pick := r.IntN(total)
	lo, hi := 0, len(cum)-1
	for lo < hi {
		mid := (lo + hi) / 2
		if cum[mid] > pick {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	bucket := buckets[lo]
	return bucket.postals[r.IntN(len(bucket.postals))], true
}

// haversineKm returns the great-circle distance in km between two
// lat/lng points. Standard formula; accuracy is fine at the city-
// distance scale.
func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371.0
	dlat := (lat2 - lat1) * math.Pi / 180
	dlng := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dlat/2)*math.Sin(dlat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dlng/2)*math.Sin(dlng/2)
	return 2 * R * math.Asin(math.Sqrt(a))
}

// sortBuckets sorts in place by (population desc, name asc). Inlined
// to avoid importing "sort" just for one call (keeping the package
// minimal-import).
func sortBuckets(buckets []cityBucket) {
	// Simple insertion sort — n is small per country (a few thousand at
	// most) and we want deterministic output.
	for i := 1; i < len(buckets); i++ {
		j := i
		for j > 0 && cityBucketLess(buckets[j], buckets[j-1]) {
			buckets[j], buckets[j-1] = buckets[j-1], buckets[j]
			j--
		}
	}
}

func cityBucketLess(a, b cityBucket) bool {
	if a.population != b.population {
		return a.population > b.population
	}
	return a.name < b.name
}

// Load reads postal_codes.tsv and returns an Index.
//
// Expected format: tab-separated, first line is a header, 12 columns:
//   country_code, postal_code, place_name, admin_name1, admin_code1,
//   admin_name2, admin_code2, admin_name3, admin_code3,
//   latitude, longitude, accuracy
func Load(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open postal_codes: %w", err)
	}
	defer f.Close()

	idx := &Index{byCountry: make(map[string][]PostalCode)}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo == 1 {
			continue // skip header
		}
		line := scanner.Text()
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 11 {
			continue
		}
		lat, _ := strconv.ParseFloat(fields[9], 64)
		lng, _ := strconv.ParseFloat(fields[10], 64)
		pc := PostalCode{
			Country:    fields[0],
			Postal:     fields[1],
			City:       fields[2],
			Region:     fields[3],
			AdminCode1: fields[4],
			AdminCode2: fields[6],
			Latitude:   lat,
			Longitude:  lng,
		}
		idx.byCountry[pc.Country] = append(idx.byCountry[pc.Country], pc)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read postal_codes: %w", err)
	}
	if len(idx.byCountry) == 0 {
		return nil, fmt.Errorf("no postal-code rows loaded from %s", path)
	}
	return idx, nil
}
