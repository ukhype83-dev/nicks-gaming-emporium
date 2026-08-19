package web

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"emporium/internal/rng"
)

const bankDir = "../../../seed_data/web"

func loadBanksT(t *testing.T) *Banks {
	t.Helper()
	b, err := LoadBanks(bankDir)
	if err != nil {
		t.Fatalf("LoadBanks: %v", err)
	}
	return b
}

func testCtx(title string) Context {
	return Context{
		"title": title, "platform": "PlayStation 2", "genre": "action",
		"publisher": "Big Publisher Co.", "developer": "Small Studio",
		"price": "$19.99", "city": "Evanston", "n_years": "40",
		"credit_amount": "$89", "year": "2008", "month_name": "March",
		"condition_grade": "Used - Good", "prev_user": "GruffJustGruff",
		"parent_user": "n64kid",
	}
}

// Every skeleton must render a non-empty, brace-free body at every year in
// the archetype's active window — bank gaps degrade to blander prose, never
// to dead ends or leaked "{slot}" text.
func TestEverySkeletonRendersClean(t *testing.T) {
	b := loadBanksT(t)
	years := []int{2005, 2006, 2008, 2011, 2013, 2015, 2016}
	for key, sks := range b.skeletons {
		archID := strings.Split(key, "|")[0]
		sent := strings.Split(key, "|")[1]
		arch := b.archByID[archID]
		for si := range sks {
			for _, yr := range years {
				if yr < arch.EraFrom || yr > arch.EraTo {
					continue
				}
				r := rng.Derive(42, fmt.Sprintf("test/%s/%d/%d", key, si, yr))
				rev := b.GenerateReview(r, arch, 3, sent, yr, testCtx("Memory Manor"))
				if strings.TrimSpace(rev.Body) == "" {
					t.Errorf("%s year %d: empty body", key, yr)
				}
				if strings.ContainsAny(rev.Body+rev.Title, "{}") {
					t.Errorf("%s year %d: unexpanded slot in %q / %q", key, yr, rev.Title, rev.Body)
				}
			}
		}
	}
}

// Same seed, same everything → byte-identical output. The whole point.
func TestDeterminism(t *testing.T) {
	b1, b2 := loadBanksT(t), loadBanksT(t)
	for i := 0; i < 50; i++ {
		ns := fmt.Sprintf("web/review/%d", i)
		a := b1.archByID["essayist"]
		r1 := b1.GenerateReview(rng.Derive(42, ns), a, 3, "mixed", 2010, testCtx("Chrono Trigger"))
		r2 := b2.GenerateReview(rng.Derive(42, ns), a, 3, "mixed", 2010, testCtx("Chrono Trigger"))
		if r1 != r2 {
			t.Fatalf("review %d not deterministic:\n%v\n%v", i, r1, r2)
		}
	}
}

func TestReceptionScoring(t *testing.T) {
	b := loadBanksT(t)
	idx := BuildReceptionIndex(b.Reception)

	if s := idx.Score("the legend of zelda: ocarina of time", "Nintendo 64", 1); s != 4.7 {
		t.Errorf("ocarina score = %v, want 4.7", s)
	}
	// Prefix rule: the multi-region denorm row still matches its base title…
	if s := idx.Score("shadow of the colossus • wander to kyozou jp, ko", "PlayStation 2", 2); s != 4.7 {
		t.Errorf("denorm-tail score = %v, want 4.7", s)
	}
	// …but a different game that shares a prefix must NOT.
	if s := idx.Score("driver 2", "PlayStation", 3); s == tierScore["good"] {
		t.Errorf("'driver 2' matched 'driver' — prefix rule too loose")
	}
	// Platform-qualified: superman is only a disaster on the N64.
	if s := idx.Score("superman", "Nintendo 64", 4); s != 1.4 {
		t.Errorf("superman N64 = %v, want 1.4", s)
	}
	if s := idx.Score("superman", "Genesis", 5); s == 1.4 {
		t.Errorf("superman Genesis inherited the N64 disaster tier")
	}
	// Hash prior: bounded, deterministic, non-uniform.
	seen := map[float64]bool{}
	for id := int64(100); id < 200; id++ {
		p := hashPrior(id)
		if p < 2.6 || p > 4.4 {
			t.Fatalf("hash prior %v out of range", p)
		}
		seen[p] = true
	}
	if len(seen) < 50 {
		t.Errorf("hash prior collapsing: only %d distinct values in 100", len(seen))
	}
}

// Troll inversion: a masterpiece should skew low for trolls, high for
// everyone else; era morale must drag the 2016 average below the 2008 one.
func TestSentimentShape(t *testing.T) {
	b := loadBanksT(t)
	troll, fan := b.archByID["troll"], b.archByID["superfan"]
	avg := func(a Archetype, year int) float64 {
		sum := 0
		for i := 0; i < 400; i++ {
			r := rng.Derive(42, fmt.Sprintf("test/sent/%s/%d/%d", a.ID, year, i))
			rating, _ := RateReview(r, a, 4.7, year)
			sum += rating
		}
		return float64(sum) / 400
	}
	if at, af := avg(troll, 2008), avg(fan, 2008); at >= af-1.0 {
		t.Errorf("troll avg %.2f not clearly below superfan %.2f on a masterpiece", at, af)
	}
	base := b.archByID["essayist"]
	if a08, a16 := avg(base, 2008), avg(base, 2016); a16 >= a08-0.4 {
		t.Errorf("era morale too weak: 2008 avg %.2f vs 2016 %.2f", a08, a16)
	}
}

func TestUsernames(t *testing.T) {
	b := loadBanksT(t)
	taken := map[string]bool{}
	baseCount := map[string]int{}
	styles := map[string]int{}
	for i := 0; i < 2000; i++ {
		r := rng.Derive(42, fmt.Sprintf("test/user/%d", i))
		in := UsernameInput{FirstName: "Paul", BirthYear: 1982, SignupYear: 2001 + i%16}
		name := MakeUnique(r, taken, baseCount, GenerateUsername(r, b, in))
		if name == "" || strings.ContainsAny(name, " \t") {
			t.Fatalf("bad handle %q", name)
		}
		switch {
		case strings.HasPrefix(name, "Paul198"):
			styles["leak_birth"]++
		case strings.HasPrefix(name, "Paul20"):
			styles["leak_signup"]++
		case strings.Contains(name, "_"):
			styles["underscore"]++
		default:
			styles["other"]++
		}
	}
	if len(taken) != 2000 {
		t.Fatalf("uniqueness failed: %d distinct of 2000", len(taken))
	}
	// The PII leak must exist and skew to birth years (anomaly A5).
	if styles["leak_birth"] < 300 || styles["leak_birth"] < styles["leak_signup"] {
		t.Errorf("A5 leak shape off: %v", styles)
	}
}

// TestMakeUniqueOversubscribed reproduces the 3T hang: a single base wanted by
// far more accounts than the old fixed ~10,009-name space could hold. The old
// implementation infinite-looped once the space saturated; the fix must
// terminate and keep every handle unique. (If this regresses, the test hangs
// rather than fails — that is the bug it guards.)
func TestMakeUniqueOversubscribed(t *testing.T) {
	taken := map[string]bool{}
	baseCount := map[string]int{}
	const n = 50000 // ~5× the old ceiling
	for i := 0; i < n; i++ {
		r := rng.Derive(42, fmt.Sprintf("test/oversub/%d", i))
		name := MakeUnique(r, taken, baseCount, "cyberwolf")
		if name == "" || strings.ContainsAny(name, " \t") {
			t.Fatalf("bad handle %q at %d", name, i)
		}
	}
	if len(taken) != n {
		t.Fatalf("uniqueness failed under oversubscription: %d distinct of %d", len(taken), n)
	}
	if baseCount["cyberwolf"] != n {
		t.Fatalf("baseCount off: got %d want %d", baseCount["cyberwolf"], n)
	}
}

// TestGenerateSampleSheet writes SAMPLES.md — the editorial-gate artifact.
// Deterministic, so the file only changes when banks or engine change.
func TestGenerateSampleSheet(t *testing.T) {
	b := loadBanksT(t)
	idx := BuildReceptionIndex(b.Reception)
	var sb strings.Builder
	sb.WriteString("# Generated sample reviews — engine smoke output\n")
	sb.WriteString("<!-- Regenerated by TestGenerateSampleSheet; deterministic at seed 42. -->\n")
	sb.WriteString("<!-- Editorial gate: read against NGE_V1.26_WEB_DESIGN.md Appendix A. -->\n\n")

	samples := []struct {
		title, platform string
		releaseID       int64
		year            int
	}{
		{"the legend of zelda: ocarina of time", "Nintendo 64", 41991, 2006},
		{"halo 2", "Xbox", 50001, 2005},
		{"too human", "Xbox 360", 41900, 2008},
		{"wii sports", "Wii", 50002, 2008},
		{"heavy rain", "PlayStation 3", 50003, 2010},
		{"some forgotten platformer", "PlayStation 2", 31337, 2007},
		{"grand theft auto v", "PlayStation 3", 50004, 2013},
		{"memory manor", "NES", 50005, 2016},
	}
	n := 0
	for _, s := range samples {
		base := idx.Score(s.title, s.platform, s.releaseID)
		for _, a := range b.Archetypes {
			if s.year < a.EraFrom || s.year > a.EraTo {
				continue
			}
			n++
			if n%3 != 0 && a.ID != "troll" && a.ID != "bucks_victim" {
				continue // keep the sheet readable: ~1/3 of combos
			}
			r := rng.Derive(42, fmt.Sprintf("web/sample/%s/%d/%s", a.ID, s.releaseID, s.title))
			rating, sent := RateReview(r, a, base, s.year)
			ctx := testCtx(s.title)
			ctx["platform"] = s.platform
			ctx["year"] = fmt.Sprintf("%d", s.year)
			rev := b.GenerateReview(r, a, rating, sent, s.year, ctx)
			user := GenerateUsername(r, b, UsernameInput{FirstName: "Dana", BirthYear: 1979, SignupYear: s.year - 2, IsTroll: a.ID == "troll"})
			stars := strings.Repeat("★", rating) + strings.Repeat("☆", 5-rating)
			fmt.Fprintf(&sb, "**%s — `%s` (%s, %d)** — *%s*\n", stars, user, a.ID, s.year, s.title)
			if rev.Title != "" {
				fmt.Fprintf(&sb, "Title: %q\n", rev.Title)
			}
			fmt.Fprintf(&sb, "> %s\n\n", rev.Body)
		}
	}
	// A short comment thread against a negative parent review.
	sb.WriteString("---\n## Sample thread (parent sentiment: neg, 2012)\n\n")
	for i, kind := range []string{"bomb_callout", "bomb_defend", "defend", "troll_reply", "dunk"} {
		archID := []string{"grouch", "capslock", "superfan", "troll", "driveby"}[i]
		r := rng.Derive(42, fmt.Sprintf("web/sample/thread/%d", i))
		c := b.GenerateComment(r, kind, b.archByID[archID], "neg", 2012, testCtx("some game"))
		fmt.Fprintf(&sb, "- **%s** (%s): %s\n", archID, kind, c)
	}
	sb.WriteString("\n## Goodbye thread (2016)\n\n")
	for i := 0; i < 4; i++ {
		r := rng.Derive(42, fmt.Sprintf("web/sample/goodbye/%d", i))
		c := b.GenerateComment(r, "goodbye", b.archByID["essayist"], "pos", 2016, testCtx("memory manor"))
		fmt.Fprintf(&sb, "- %s\n", c)
	}

	if err := os.WriteFile("SAMPLES.md", []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write samples: %v", err)
	}
}
