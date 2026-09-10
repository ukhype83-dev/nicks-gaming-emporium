// The reception model (design §4b): every reviewable item gets a
// deterministic "true score" — curated Layer 1 for notable titles, derived
// Layer 2 for the rest — which archetype bias, era morale and per-review
// noise then perturb into a rating and sentiment.
package web

import (
	"hash/fnv"
	"math/rand/v2"
	"strings"
)

// Tier score anchors (reception.tsv header is the canonical statement).
var tierScore = map[string]float64{
	"masterpiece": 4.7,
	"great":       4.3,
	"good":        3.8,
	"mixed":       3.0,
	"poor":        2.2,
	"disaster":    1.4,
}

// ReceptionIndex resolves (normalised_title, platform) → base score.
type ReceptionIndex struct {
	exact map[string]float64 // "title|platform" and "title|any"
	// prefix rows are checked with the denorm/regional-tail rule; kept as a
	// slice because prefix lookup can't be a map hit.
	prefixes []receptionPrefix
}

type receptionPrefix struct {
	title    string
	platform string
	score    float64
}

// BuildReceptionIndex compiles the curated rows for lookup.
func BuildReceptionIndex(rows []ReceptionRow) *ReceptionIndex {
	idx := &ReceptionIndex{exact: make(map[string]float64, len(rows)*2)}
	for _, r := range rows {
		s, ok := tierScore[r.Tier]
		if !ok {
			continue
		}
		idx.exact[r.Title+"|"+r.Platform] = s
		idx.prefixes = append(idx.prefixes, receptionPrefix{r.Title, r.Platform, s})
	}
	return idx
}

// Score returns the Layer-1 curated score for a catalog row, or the Layer-2
// derived prior when the title is uncurated. The match rule mirrors the
// reception.tsv header: exact, or the catalog title continues with a
// denorm/regional tail (' •', ' /', ' na', ' pal', ' jp', ' eu').
//
// Layer 2 here is the hash prior + smoothing placeholder; the trade-in
// velocity and sales-longevity signals land when generation is wired to the
// replayed transaction streams (design §4b Layer 2, load-phase work).
func (idx *ReceptionIndex) Score(normTitle, platform string, releaseID int64) float64 {
	t := strings.ToLower(strings.TrimSpace(normTitle))
	if s, ok := idx.exact[t+"|"+platform]; ok {
		return s
	}
	if s, ok := idx.exact[t+"|any"]; ok {
		return s
	}
	for _, p := range idx.prefixes {
		if p.platform != "any" && p.platform != platform {
			continue
		}
		if rest, ok := strings.CutPrefix(t, p.title); ok && tailIsDenorm(rest) {
			return p.score
		}
	}
	return hashPrior(releaseID)
}

// IsCurated reports whether a title/platform resolves to a Layer-1 curated
// reception tier (as opposed to the hash prior). Used to build the pool of
// famous releases for unverified reviews.
func (idx *ReceptionIndex) IsCurated(normTitle, platform string) bool {
	t := strings.ToLower(strings.TrimSpace(normTitle))
	if _, ok := idx.exact[t+"|"+platform]; ok {
		return true
	}
	if _, ok := idx.exact[t+"|any"]; ok {
		return true
	}
	for _, p := range idx.prefixes {
		if p.platform != "any" && p.platform != platform {
			continue
		}
		if rest, ok := strings.CutPrefix(t, p.title); ok && tailIsDenorm(rest) {
			return true
		}
	}
	return false
}

// tailIsDenorm reports whether what follows a curated title is a multi-region
// denorm tail rather than a different game ("driver" must not match
// "driver 2").
func tailIsDenorm(rest string) bool {
	if rest == "" {
		return true // exact (already handled, but harmless)
	}
	for _, pfx := range []string{" •", " /", ":", " na", " pal", " jp", " eu", " †"} {
		if strings.HasPrefix(rest, pfx) {
			return true
		}
	}
	return false
}

// hashPrior spreads uncurated titles across [2.6, 4.4] deterministically —
// obscure games disagree with each other, nothing is exactly average.
func hashPrior(releaseID int64) float64 {
	h := fnv.New64a()
	var buf [8]byte
	for i := 0; i < 8; i++ {
		buf[i] = byte(uint64(releaseID) >> (8 * i))
	}
	h.Write(buf[:])
	h.Write([]byte("web/reception/prior"))
	return 2.6 + float64(h.Sum64()%1800)/1000.0
}

// EraMorale is the community-wide modifier that produces anomaly A1: the
// average drifts as the company declines — it is NGE being reviewed, not the
// games. Zero through the boom, then a steepening slide.
func EraMorale(year int) float64 {
	switch {
	case year <= 2010:
		return 0
	case year <= 2012:
		return -0.12 * float64(year-2010) // 2011 −0.12, 2012 −0.24
	case year <= 2015:
		return -0.24 - 0.18*float64(year-2012) // 2013 −0.42 … 2015 −0.78
	default:
		return -1.05 // 2016: the fire-sale year
	}
}

// RateReview turns a base score into (rating, sentiment) for one review.
// Trolls invert around the midpoint (design §4: rating what you remember,
// not what it deserves — in reverse).
func RateReview(r *rand.Rand, arch Archetype, baseScore float64, year int) (int, string) {
	score := baseScore
	if arch.ID == "troll" {
		score = 6 - score
	}
	score += arch.RatingBias + EraMorale(year) + r.NormFloat64()*0.55

	rating := int(score + 0.5)
	if rating < 1 {
		rating = 1
	}
	if rating > 5 {
		rating = 5
	}
	// J-shape: extremes attract (people write when they feel strongly).
	if rating == 4 && r.Float64() < 0.25 {
		rating = 5
	}
	if rating == 2 && r.Float64() < 0.30 {
		rating = 1
	}
	sent := "mixed"
	if rating >= 4 {
		sent = "pos"
	} else if rating <= 2 {
		sent = "neg"
	}
	return rating, sent
}
