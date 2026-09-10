// V1.27 genre canonicalisation. The scraped catalog carried ~2,000 distinct
// genres (2D/3D splits, compound "X, Y, Z" tails, case/wording dupes, and a
// long tail of non-game scrape noise — music genres, "Fantasy Film", anime
// tags). This maps each raw genre to one of ~109 canonical game genres
// (CRPG/JRPG/Action-RPG/Tactical kept distinct), routing pure noise to NULL.
//
// Applied ONLY at the dbo.releases write (ReleasesToRows). The web layer keeps
// the raw genre in review/comment prose, so canonicalisation never perturbs
// the web RNG stream.
package dbwriter

import (
	_ "embed"
	"strings"
)

//go:embed genre_map.tsv
var genreMapRaw string

// genreCanon: raw genre -> canonical. A present entry with an empty value
// means "drop to NULL" (scrape noise that isn't a game genre).
var genreCanon = buildGenreCanon()

func buildGenreCanon() map[string]string {
	m := make(map[string]string, 2048)
	for _, line := range strings.Split(genreMapRaw, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		m[parts[0]] = parts[1] // parts[1] may be "" -> NULL
	}
	return m
}

// canonicalGenre returns the DB value for a raw catalog genre (nil = NULL).
// Raw values absent from the map (should not occur for the seeded catalog)
// pass through unchanged.
func canonicalGenre(raw string) any {
	if raw == "" {
		return nil
	}
	if c, ok := genreCanon[raw]; ok {
		if c == "" {
			return nil
		}
		return c
	}
	return raw
}
