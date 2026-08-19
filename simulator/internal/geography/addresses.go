// Locale-aware synthetic street-address generation.
//
// Composes addresses as: real postal code + real city (GeoNames) +
// real street name (OSM, V1.12+) + synthetic street number. Result:
// an address that locates correctly on a map (every component is
// genuine for its city) but doesn't refer to a real building.
//
// V1.12 changed street-name sourcing from a hand-curated 10-name
// per-country list to a real per-city OSM extract:
// seed_data/city_streets.tsv carries the top ~100 street names per
// city for the top 50 cities of each of the 18 NGE countries, ranked
// by OSM frequency (proxy for how prominent the street is). Within a
// city, sampling is weighted by that frequency — so iconic streets
// (Deansgate in Manchester, Old Kent Road in London) come up more
// often than obscure cul-de-sacs.
//
// When the address city is NOT one of the top-50 cities for its
// country, sampling falls back to the original per-country generic
// list (still locale-aware: "High Street" / "Hauptstraße" /
// "rue du Marché" etc).
package geography

import (
	"bufio"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
)

// addressFormat describes how to compose "{number} {street}" for a
// given country.
type addressFormat int

const (
	fmtNumberThenStreet addressFormat = iota // "127 Oxford Street" (US/GB/FR/AU/CA)
	fmtStreetThenNumber                      // "Hauptstraße 47"    (DE/NL/CH/SE/NO/DK/PL/CZ)
	fmtStreetCommaNumber                     // "Calle Mayor, 23"   (ES/IT/BR)
	fmtChomeBan                              // "2-3-4 Shibuya"     (JP/KR)
)

type countryAddress struct {
	format      addressFormat
	streets     []string
	numberRange [2]int // inclusive; commercial-street range
}

// Street-name banks are deliberately small for the skeleton — 8–12
// plausibly-commercial names per country is enough for visible
// variety. Population-weighted realism and longer dictionaries come
// in V1.5.
var countryAddresses = map[string]countryAddress{
	"US": {
		format:      fmtNumberThenStreet,
		numberRange: [2]int{1, 9950},
		streets: []string{
			"Main Street", "Broadway", "Market Street", "Commerce Street",
			"State Street", "Michigan Avenue", "5th Avenue", "Oak Street",
			"Walnut Street", "Madison Avenue", "Park Avenue",
		},
	},
	"GB": {
		format:      fmtNumberThenStreet,
		numberRange: [2]int{1, 4000},
		streets: []string{
			"High Street", "King Street", "Queen Street", "Oxford Street",
			"Regent Street", "Bond Street", "Station Road", "Church Street",
			"Market Square", "The Mall", "Commercial Road",
		},
	},
	"AU": {
		format:      fmtNumberThenStreet,
		numberRange: [2]int{1, 5000},
		streets: []string{
			"George Street", "Bourke Street", "Collins Street", "Queen Street",
			"Elizabeth Street", "Pitt Street", "King William Street", "Hay Street",
			"Swanston Street", "Rundle Mall",
		},
	},
	"CA": {
		format:      fmtNumberThenStreet,
		numberRange: [2]int{1, 9950},
		streets: []string{
			"Yonge Street", "Queen Street", "King Street", "Bloor Street",
			"Robson Street", "Sainte-Catherine Street", "Granville Street",
			"Main Street", "Bay Street", "Saint-Laurent Boulevard",
		},
	},
	"FR": {
		format:      fmtNumberThenStreet,
		numberRange: [2]int{1, 3000},
		streets: []string{
			"rue de Rivoli", "rue Saint-Honoré", "rue du Commerce",
			"avenue des Champs-Élysées", "rue Bonaparte", "boulevard Haussmann",
			"rue de la République", "rue Nationale", "place du Marché",
			"rue de la Paix",
		},
	},
	"DE": {
		format:      fmtStreetThenNumber,
		numberRange: [2]int{1, 2000},
		streets: []string{
			"Hauptstraße", "Königstraße", "Friedrichstraße", "Kaiserstraße",
			"Marktplatz", "Schlossstraße", "Bahnhofstraße", "Goethestraße",
			"Lindenstraße", "Mühlenstraße",
		},
	},
	"NL": {
		format:      fmtStreetThenNumber,
		numberRange: [2]int{1, 1800},
		streets: []string{
			"Kalverstraat", "Damrak", "Rokin", "Leidsestraat", "Singel",
			"Nieuwendijk", "Grote Markt", "Hoogstraat", "Lijnbaan", "Coolsingel",
		},
	},
	"CH": {
		format:      fmtStreetThenNumber,
		numberRange: [2]int{1, 1500},
		streets: []string{
			"Bahnhofstrasse", "Rennweg", "Kramgasse", "Marktgasse",
			"Rue du Mont-Blanc", "Rue du Rhône", "Spalenberg", "Niederdorfstrasse",
		},
	},
	"SE": {
		format:      fmtStreetThenNumber,
		numberRange: [2]int{1, 1500},
		streets: []string{
			"Storgatan", "Drottninggatan", "Kungsgatan", "Hamngatan",
			"Västra Hamngatan", "Sveavägen", "Götgatan", "Birger Jarlsgatan",
		},
	},
	"NO": {
		format:      fmtStreetThenNumber,
		numberRange: [2]int{1, 1500},
		streets: []string{
			"Karl Johans gate", "Storgata", "Bogstadveien", "Grensen",
			"Torggata", "Markens gate", "Strandgaten", "Akersgata",
		},
	},
	"DK": {
		format:      fmtStreetThenNumber,
		numberRange: [2]int{1, 1500},
		streets: []string{
			"Strøget", "Købmagergade", "Østergade", "Vesterbrogade",
			"Nørrebrogade", "Amagerbrogade", "Frederiksberggade", "Gothersgade",
		},
	},
	"PL": {
		format:      fmtStreetThenNumber,
		numberRange: [2]int{1, 1500},
		streets: []string{
			"ulica Marszałkowska", "ulica Nowy Świat", "ulica Krakowskie Przedmieście",
			"ulica Floriańska", "ulica Długa", "ulica Świętojańska",
			"ulica Piotrkowska", "ulica Chmielna",
		},
	},
	"CZ": {
		format:      fmtStreetThenNumber,
		numberRange: [2]int{1, 1500},
		streets: []string{
			"Václavské náměstí", "Na Příkopě", "Pařížská", "Národní",
			"Celetná", "Karlova", "Wenceslas Square", "Staroměstské náměstí",
		},
	},
	"ES": {
		format:      fmtStreetCommaNumber,
		numberRange: [2]int{1, 3000},
		streets: []string{
			"Calle Mayor", "Gran Vía", "Avenida de América", "Paseo de la Castellana",
			"Calle de Alcalá", "Calle de Serrano", "Rambla de Cataluña",
			"Paseo de Gracia", "Calle de Preciados",
		},
	},
	"IT": {
		format:      fmtStreetCommaNumber,
		numberRange: [2]int{1, 3000},
		streets: []string{
			"Via del Corso", "Via Condotti", "Via Dante", "Via Nazionale",
			"Via Roma", "Corso Buenos Aires", "Via Montenapoleone",
			"Piazza del Duomo", "Via Veneto",
		},
	},
	"BR": {
		format:      fmtStreetCommaNumber,
		numberRange: [2]int{1, 3000},
		streets: []string{
			"Avenida Paulista", "Rua Oscar Freire", "Rua 25 de Março",
			"Avenida Atlântica", "Rua XV de Novembro", "Avenida Brigadeiro Faria Lima",
			"Rua Augusta", "Avenida Rio Branco", "Rua do Ouvidor",
		},
	},
	"JP": {
		format:      fmtChomeBan,
		numberRange: [2]int{1, 9}, // for chome/ban/go parts
		streets:     nil,          // JP uses structural formatting, not street names
	},
	"KR": {
		format:      fmtChomeBan,
		numberRange: [2]int{1, 9},
		streets:     nil,
	},
}

// cityStreets[country][city] = flat list of street names, where each
// street is repeated according to its OSM frequency. Uniform sampling
// from this list yields frequency-weighted picks naturally.
//
// Populated by LoadCityStreets. nil = no OSM data loaded → fall back
// to genericStreets or the editorial per-country list in
// countryAddresses.
var cityStreets map[string]map[string][]string

// genericStreets[country] = list of street names that appear in many
// cities of that country (mined from city_streets.tsv by
// build_seed_generic_streets.py). Used as a secondary fallback when
// the customer's specific city is outside the top-50 OSM-covered set —
// gives long-tail customers data-driven generic names ("Station Road",
// "Main Street", "Hauptstraße", "Via Roma") instead of the
// editorial 10-name list.
//
// Populated by LoadGenericStreets. nil = use editorial fallback.
var genericStreets map[string][]string

// LoadCityStreets reads seed_data/city_streets.tsv built by
// build_seed_streets.py. Each row: country_code, city_name, street_name,
// osm_count. The osm_count is used as a frequency multiplier so iconic
// streets get more draws.
//
// If the file is absent, returns nil silently — the simulator falls
// back to V1.11 behaviour (generic per-country street names).
func LoadCityStreets(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // optional
		}
		return fmt.Errorf("open city streets: %w", err)
	}
	defer f.Close()

	loaded := make(map[string]map[string][]string)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo == 1 {
			continue // header
		}
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 4 {
			continue
		}
		country, city, street := fields[0], fields[1], fields[2]
		// Cap the per-row multiplier so a single dominant street can't
		// crowd out the rest of the city. The TSV's top streets often
		// have counts of 100-500; cap at 20 keeps variety reasonable.
		count := 0
		for _, ch := range fields[3] {
			if ch < '0' || ch > '9' {
				break
			}
			count = count*10 + int(ch-'0')
		}
		if count <= 0 {
			count = 1
		}
		if count > 20 {
			count = 20
		}
		byCity, ok := loaded[country]
		if !ok {
			byCity = make(map[string][]string)
			loaded[country] = byCity
		}
		bucket := byCity[city]
		for i := 0; i < count; i++ {
			bucket = append(bucket, street)
		}
		byCity[city] = bucket
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read city streets: %w", err)
	}
	cityStreets = loaded
	return nil
}

// CityStreetCount returns the loaded (country, city) bucket size, or
// 0 if not loaded. Useful for telemetry / diagnostics.
func CityStreetCount(country, city string) int {
	if cityStreets == nil {
		return 0
	}
	return len(cityStreets[country][city])
}

// LoadGenericStreets reads seed_data/generic_streets.tsv built by
// build_seed_generic_streets.py. Each row: country_code, street_name,
// num_cities. The num_cities count is used as a frequency weight (a
// street appearing in 33 cities is more "common everywhere" than one
// in 3 cities), capped at 20 for the same crowd-out reason as
// LoadCityStreets.
//
// Absent file → returns nil silently; behaviour falls back to the
// editorial per-country list in countryAddresses.
func LoadGenericStreets(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open generic streets: %w", err)
	}
	defer f.Close()

	loaded := make(map[string][]string)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo == 1 {
			continue // header
		}
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 3 {
			continue
		}
		country := fields[0]
		street := fields[1]
		count := 0
		for _, ch := range fields[2] {
			if ch < '0' || ch > '9' {
				break
			}
			count = count*10 + int(ch-'0')
		}
		if count <= 0 {
			count = 1
		}
		if count > 20 {
			count = 20
		}
		bucket := loaded[country]
		for i := 0; i < count; i++ {
			bucket = append(bucket, street)
		}
		loaded[country] = bucket
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read generic streets: %w", err)
	}
	genericStreets = loaded
	return nil
}

// pickStreetName returns the street part of an address using a
// three-tier fallback ladder:
//
//	1. cityStreets[country][city]  — OSM-sourced real streets for top-50
//	                                 cities (V1.12+)
//	2. genericStreets[country]     — OSM-mined cross-city common names
//	                                 (V1.13.1+; covers the long tail)
//	3. cfg.streets                 — editorial 10-name fallback (pre-V1.12)
//
// Each tier is checked in order; first non-empty wins.
// compassPoints expand street-name variety in grid-directional locales.
var compassPoints = []string{
	"North", "South", "East", "West",
	"Northeast", "Northwest", "Southeast", "Southwest",
}

// directionalLocale marks countries whose street grids commonly carry a
// compass prefix ("North 5th Street"). Confined to North-American / AU
// grid cultures; UK/continental naming doesn't work this way.
var directionalLocale = map[string]bool{"US": true, "CA": true, "AU": true}

func startsWithDirection(name string) bool {
	for _, d := range compassPoints {
		if strings.HasPrefix(name, d+" ") {
			return true
		}
	}
	return false
}

func pickBaseStreetName(country, city string, cfg countryAddress, r *rand.Rand) string {
	if cityStreets != nil {
		if byCity, ok := cityStreets[country]; ok {
			if bucket, ok := byCity[city]; ok && len(bucket) > 0 {
				return bucket[r.IntN(len(bucket))]
			}
		}
	}
	if genericStreets != nil {
		if bucket, ok := genericStreets[country]; ok && len(bucket) > 0 {
			return bucket[r.IntN(len(bucket))]
		}
	}
	if len(cfg.streets) == 0 {
		return ""
	}
	return cfg.streets[r.IntN(len(cfg.streets))]
}

// pickStreetName returns a street name, expanding variety in
// grid-directional locales. V1.27: the OSM seed carries only ~4k US base
// names across 50 cities, so line1 alone was heavily shared. ~44% of
// US/CA/AU streets now take a compass prefix (one RNG draw), multiplying
// the distinct-name pool ~9x while staying realistic.
func pickStreetName(country, city string, cfg countryAddress, r *rand.Rand) string {
	name := pickBaseStreetName(country, city, cfg, r)
	if name != "" && directionalLocale[country] && !startsWithDirection(name) {
		if d := r.IntN(18); d < len(compassPoints) {
			name = compassPoints[d] + " " + name
		}
	}
	return name
}

// GenerateStreetAddress produces a synthetic street line (line1) for
// the given country + city, using the supplied RNG. The city argument
// is V1.12+; passing "" forces the per-country generic fallback.
// Returns a placeholder if the country is unknown — should never
// happen in practice since countryAddresses covers all of ShopShares.
func GenerateStreetAddress(country, city string, r *rand.Rand) string {
	cfg, ok := countryAddresses[country]
	if !ok {
		return fmt.Sprintf("Unknown-Country-Street %d", r.IntN(500)+1)
	}
	switch cfg.format {
	case fmtChomeBan:
		// JP/KR: hierarchical chome-ban-go numbers — no street name.
		// OSM street names for these countries are mostly empty in
		// any case (Japan addresses are block-based).
		return fmt.Sprintf("%d-%d-%d",
			r.IntN(cfg.numberRange[1])+1,
			r.IntN(cfg.numberRange[1])+1,
			r.IntN(cfg.numberRange[1])+1)
	case fmtStreetThenNumber:
		street := pickStreetName(country, city, cfg, r)
		num := r.IntN(cfg.numberRange[1]-cfg.numberRange[0]+1) + cfg.numberRange[0]
		return fmt.Sprintf("%s %d", street, num)
	case fmtStreetCommaNumber:
		street := pickStreetName(country, city, cfg, r)
		num := r.IntN(cfg.numberRange[1]-cfg.numberRange[0]+1) + cfg.numberRange[0]
		return fmt.Sprintf("%s, %d", street, num)
	default: // fmtNumberThenStreet
		street := pickStreetName(country, city, cfg, r)
		num := r.IntN(cfg.numberRange[1]-cfg.numberRange[0]+1) + cfg.numberRange[0]
		return fmt.Sprintf("%d %s", num, street)
	}
}
