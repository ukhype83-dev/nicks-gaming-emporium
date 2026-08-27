package web

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// V1.27 review-immersion invariants. These assert the requires-gating actually
// holds on generated prose: a cartridge title never gets disc-only flavour (and
// vice-versa), single-platform titles never imply a sibling release, retro
// framing only fires for genuinely old games, and censored profanity appears
// only in the ragey voices. All are properties of the phrase gating, so they
// are checked over many generated bodies rather than one.

// disc-only / cart-only signature fragments (authored, medium-gated). Bare
// "disc"/"cart" are deliberately NOT here — a cart title may still carry the
// ungated "cereal-box demo disc" metaphor, which is not a medium claim.
var discOnlySigs = []string{
	"resurfaced", "rattled in the case", "cracked out of the shrink-wrap",
	"at the disc level", "Disc is flawless", "complete on disc",
	"is the disc complete", "disc complete or download",
}
var cartOnlySigs = []string{
	"pins clean", "battery holding a save", "contacts a little tarnished",
	"save batteries die",
}

// other-version signatures (authored, has_other_versions-gated).
var otherVersionSigs = []string{
	"version first; I liked", "renders at a lower internal resolution than this one",
	"worse edition", "delta over the", "version has the same issue",
}

// retro signatures (authored, retro-gated) — unique to the retro rows.
var retroSigs = []string{
	"clearing the backlog", "retrospective, not a launch take",
	"getting its turn on my shelf", "years on, it is striking",
	"aged better than most", "recommend for the curious",
}

func immersionCtx(m ReleaseMeta, year int) Context {
	ctx := Context{
		"title": cleanDisplayTitle(m.Title), "platform": orDefault(m.Platform, "console"),
		"genre": orDefault(m.Genre, "game"), "publisher": orDefault(m.Publisher, "the publisher"),
		"developer": orDefault(m.Developer, "the studio"), "price": "£20",
		"city": "London", "condition_grade": "Good", "n_years": "40",
		"credit_amount": "£30", "year": itoa(int64(year)), "stars": "3",
	}
	applyReleaseImmersion(ctx, m, year)
	return ctx
}

// genBodies assembles n review bodies for one archetype across all sentiments,
// against the given release context — a broad, deterministic sweep of the pools.
func genBodies(b *Banks, archID string, m ReleaseMeta, year, n int) []string {
	arch := b.archByID[archID]
	sents := []string{"pos", "mixed", "neg"}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		r := rand.New(rand.NewPCG(uint64(i)+1, 0x9e3779b97f4a7c15))
		ctx := immersionCtx(m, year)
		rev := b.GenerateReview(r, arch, 1+r.IntN(5), sents[i%3], year, ctx)
		out = append(out, rev.Title+" ||| "+rev.Body)
	}
	return out
}

func containsAny(s string, subs []string) (string, bool) {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return sub, true
		}
	}
	return "", false
}

func TestImmersionMediumGating(t *testing.T) {
	b := loadBanksT(t)
	cart := ReleaseMeta{Title: "Blast Corps", Platform: "Nintendo 64", Genre: "action",
		Developer: "Rare", ReleaseYear: 1997, Media: "Cartridge"}
	disc := ReleaseMeta{Title: "Gran Turismo", Platform: "PlayStation", Genre: "racing",
		Developer: "Polyphony Digital", ReleaseYear: 1997, Media: "CD-ROM"}

	for _, arch := range []string{"collector", "technerd", "bucks_victim", "grouch", "essayist"} {
		for _, body := range genBodies(b, arch, cart, 2010, 300) {
			if sig, hit := containsAny(body, discOnlySigs); hit {
				t.Errorf("cartridge title got a disc-only line (%s, %q): %s", arch, sig, body)
			}
		}
		for _, body := range genBodies(b, arch, disc, 2010, 300) {
			if sig, hit := containsAny(body, cartOnlySigs); hit {
				t.Errorf("disc title got a cart-only line (%s, %q): %s", arch, sig, body)
			}
		}
	}
}

func TestImmersionOtherVersionGating(t *testing.T) {
	b := loadBanksT(t)
	solo := ReleaseMeta{Title: "Solo Only", Platform: "Nintendo 64", Genre: "action",
		Developer: "Rare", ReleaseYear: 1999, Media: "Cartridge"} // no OtherPlatforms
	multi := ReleaseMeta{Title: "Cross Platform", Platform: "PlayStation 2", Genre: "action",
		Developer: "Capcom", ReleaseYear: 2004, Media: "DVD-ROM",
		OtherPlatforms: []string{"Xbox", "Nintendo GameCube"}}

	for _, arch := range []string{"essayist", "technerd", "any", "superfan"} {
		if _, ok := b.archByID[arch]; !ok {
			continue
		}
		for _, body := range genBodies(b, arch, solo, 2010, 400) {
			if sig, hit := containsAny(body, otherVersionSigs); hit {
				t.Errorf("single-platform title implied a sibling release (%s, %q): %s", arch, sig, body)
			}
		}
	}
	// Multi-platform: {other_platform} must expand to a real sibling, never blank
	// or an unresolved slot. (Presence is probabilistic, so we don't require it.)
	for _, body := range genBodies(b, "technerd", multi, 2010, 400) {
		if strings.Contains(body, "{other_platform}") || strings.Contains(body, "the  version") {
			t.Errorf("multi-platform title left other_platform unfilled: %s", body)
		}
	}
}

func TestImmersionRetroGating(t *testing.T) {
	b := loadBanksT(t)
	old := ReleaseMeta{Title: "Old Classic", Platform: "PlayStation", Genre: "rpg",
		Developer: "Square", ReleaseYear: 1998, Media: "CD-ROM"}
	fresh := ReleaseMeta{Title: "Brand New", Platform: "PlayStation 2", Genre: "rpg",
		Developer: "Square", ReleaseYear: 2013, Media: "DVD-ROM"}

	// A 1998 game reviewed in 2014 (age 16) may be framed as retro.
	retroSeen := false
	for _, arch := range []string{"essayist", "any", "technerd"} {
		if _, ok := b.archByID[arch]; !ok {
			continue
		}
		for _, body := range genBodies(b, arch, old, 2014, 400) {
			if _, hit := containsAny(body, retroSigs); hit {
				retroSeen = true
			}
			// game_age / release_year must render as numbers if referenced.
			if strings.Contains(body, "{game_age}") || strings.Contains(body, "{release_year}") {
				t.Errorf("retro placeholder left unexpanded: %s", body)
			}
		}
	}
	if !retroSeen {
		t.Error("no retro framing ever fired for a 16-year-old game")
	}
	// A 2013 game reviewed in 2014 (age 1) must NEVER read as retro.
	for _, arch := range []string{"essayist", "any", "technerd"} {
		if _, ok := b.archByID[arch]; !ok {
			continue
		}
		for _, body := range genBodies(b, arch, fresh, 2014, 400) {
			if sig, hit := containsAny(body, retroSigs); hit {
				t.Errorf("recent game (age 1) framed as retro (%s, %q): %s", arch, sig, body)
			}
		}
	}
}

func TestImmersionCensoredConfinedToRagey(t *testing.T) {
	b := loadBanksT(t)
	m := ReleaseMeta{Title: "Some Game", Platform: "PlayStation 2", Genre: "shooter",
		Developer: "Studio", ReleaseYear: 2005, Media: "DVD-ROM"}

	ragey := map[string]bool{"troll": true, "capslock": true, "bucks_victim": true}
	// Non-ragey voices must never emit a censored ('*') token.
	for _, arch := range []string{"essayist", "collector", "technerd", "parent", "grouch", "superfan"} {
		if _, ok := b.archByID[arch]; !ok {
			continue
		}
		for _, body := range genBodies(b, arch, m, 2016, 300) {
			if strings.Contains(body, "*") {
				t.Errorf("non-ragey archetype %s emitted censored profanity: %s", arch, body)
			}
		}
	}
	// Ragey voices should produce censored profanity at least sometimes.
	for arch := range ragey {
		if _, ok := b.archByID[arch]; !ok {
			continue
		}
		seen := false
		for _, body := range genBodies(b, arch, m, 2016, 600) {
			if strings.Contains(body, "*") {
				seen = true
				break
			}
		}
		if !seen {
			t.Errorf("ragey archetype %s never produced censored profanity in 600 samples", arch)
		}
	}
}
