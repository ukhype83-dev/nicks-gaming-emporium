// Package web generates NGE's user-generated web content — accounts,
// product reviews ("The Counter"), comment threads, votes and clickstream —
// for V1.26. Design: NGE_V1.26_WEB_DESIGN.md. The text engine is a
// compositional slot grammar: phrase banks are authored at design time in
// seed_data/web/*.tsv; this package assembles them deterministically
// (rng.Derive namespaces "web/...") so a given seed always produces
// byte-identical prose. Deliberately stdlib-only, modelled on
// internal/catalog and internal/hardware so the seed pipelines stay parallel.
package web

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Archetype is one row of archetypes.tsv — a sticky per-customer reviewing
// personality. Weights condition on the customer's purchase decile via
// WhaleMult (see design §4: the die-hards are the forum rats).
type Archetype struct {
	ID          string
	Weight      float64
	WhaleMult   float64
	EraFrom     int
	EraTo       int
	RatingBias  float64
	SentMin     int
	SentMax     int
	TypoRate    float64
	CapsRate    float64
	TitleRate   float64
	CommentMult float64
}

// SlotRef is one entry of a skeleton: a slot name plus the probability the
// slot is included at all ("store_note?0.3" → {store_note, 0.3}).
type SlotRef struct {
	Name string
	Prob float64
}

// Skeleton is one row of skeletons_en.tsv — a sentence plan for one
// archetype at one sentiment.
type Skeleton struct {
	Archetype string
	Sentiment string
	Weight    float64
	Slots     []SlotRef
}

// Phrase is one row of phrases_en.tsv (or a language sibling). Requires is an
// optional 7th column (V1.27): a comma-separated predicate list the review
// context must satisfy for the row to be eligible (e.g. "media_disc",
// "has_other_versions", "retro"; "!" negates a token). Empty = always eligible,
// so pre-V1.27 six-column rows are unchanged. See requiresSatisfied.
type Phrase struct {
	Slot      string
	Archetype string // "any" = usable by all
	Sentiment string // pos|mixed|neg|any
	EraFrom   int
	EraTo     int
	Text      string
	Requires  string
}

// CommentPhrase is one row of comments_en.tsv — a conversational move. Requires
// is the same optional 7th-column predicate list as Phrase (V1.27).
type CommentPhrase struct {
	Kind       string
	Archetype  string
	ParentSent string // sentiment of the PARENT REVIEW: pos|mixed|neg|any
	EraFrom    int
	EraTo      int
	Text       string
	Requires   string
}

// UsernamePart is one row of usernames.tsv.
type UsernamePart struct {
	Style string
	Part  string
	Text  string
}

// ReceptionRow is one row of reception.tsv — the curated Layer-1 tier for a
// notable title (design §4b). Platform "any" matches every platform, else it
// must equal dbo.platforms.name exactly.
type ReceptionRow struct {
	Title    string
	Platform string
	Tier     string
}

// Banks holds every loaded seed bank, indexed for the assembler. The
// skeletons/phrases/comments maps hold the PRIMARY language (English); other
// languages live in byLang, each a sibling *Banks that shares the
// language-independent data (archetypes, usernames, reception) but carries
// its own skeletons/phrases/comments. Non-English banks are lighter and use
// archetype-agnostic ("any") skeletons — the engine falls back to them.
type Banks struct {
	Archetypes []Archetype
	archByID   map[string]Archetype

	skeletons map[string][]Skeleton // key archetype|sentiment
	phrases   map[string][]Phrase   // key slot
	comments  map[string][]CommentPhrase
	userparts map[string][]string // key style|part
	Reception []ReceptionRow

	lang   string            // language of this bank's content ("en" for primary)
	byLang map[string]*Banks // language code → sibling banks (en → self)
}

// HasLanguage reports whether a phrase bank for the given language code is
// loaded (English always is; others are present iff their _<lang> files
// were found under the bank dir).
func (b *Banks) HasLanguage(code string) bool {
	if code == "en" {
		return true
	}
	_, ok := b.byLang[code]
	return ok
}

// forLang returns the sibling banks for a language, or the English banks if
// that language isn't loaded.
func (b *Banks) forLang(code string) *Banks {
	if lb, ok := b.byLang[code]; ok {
		return lb
	}
	return b
}

// Languages returns the loaded language codes (for logging).
func (b *Banks) Languages() []string {
	out := make([]string, 0, len(b.byLang))
	for k := range b.byLang {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// LoadBanks reads all seed banks from dir (seed_data/web). Any missing file
// is an error: the banks are the engine's contract.
func LoadBanks(dir string) (*Banks, error) {
	b := &Banks{
		archByID:  map[string]Archetype{},
		skeletons: map[string][]Skeleton{},
		phrases:   map[string][]Phrase{},
		comments:  map[string][]CommentPhrase{},
		userparts: map[string][]string{},
	}
	if err := forEachRow(filepath.Join(dir, "archetypes.tsv"), 12, func(f []string) error {
		a := Archetype{
			ID:          f[0],
			Weight:      atofOr(f[1], 0),
			WhaleMult:   atofOr(f[2], 1),
			EraFrom:     atoiOr(f[3], 2005),
			EraTo:       atoiOr(f[4], 2016),
			RatingBias:  atofOr(f[5], 0),
			SentMin:     atoiOr(f[6], 1),
			SentMax:     atoiOr(f[7], 6),
			TypoRate:    atofOr(f[8], 0),
			CapsRate:    atofOr(f[9], 0),
			TitleRate:   atofOr(f[10], 0.5),
			CommentMult: atofOr(f[11], 1),
		}
		b.Archetypes = append(b.Archetypes, a)
		b.archByID[a.ID] = a
		return nil
	}); err != nil {
		return nil, err
	}
	if err := forEachRow(filepath.Join(dir, "skeletons_en.tsv"), 4, func(f []string) error {
		sk := Skeleton{Archetype: f[0], Sentiment: f[1], Weight: atofOr(f[2], 1)}
		for _, tok := range strings.Split(f[3], "|") {
			ref := SlotRef{Name: tok, Prob: 1}
			if i := strings.IndexByte(tok, '?'); i >= 0 {
				ref.Name = tok[:i]
				ref.Prob = atofOr(tok[i+1:], 1)
			}
			sk.Slots = append(sk.Slots, ref)
		}
		k := sk.Archetype + "|" + sk.Sentiment
		b.skeletons[k] = append(b.skeletons[k], sk)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := forEachRow(filepath.Join(dir, "phrases_en.tsv"), 6, func(f []string) error {
		p := Phrase{Slot: f[0], Archetype: f[1], Sentiment: f[2],
			EraFrom: atoiOr(f[3], 2005), EraTo: atoiOr(f[4], 2016), Text: f[5],
			Requires: optField(f, 6)}
		b.phrases[p.Slot] = append(b.phrases[p.Slot], p)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := forEachRow(filepath.Join(dir, "comments_en.tsv"), 6, func(f []string) error {
		c := CommentPhrase{Kind: f[0], Archetype: f[1], ParentSent: f[2],
			EraFrom: atoiOr(f[3], 2008), EraTo: atoiOr(f[4], 2016), Text: f[5],
			Requires: optField(f, 6)}
		b.comments[c.Kind] = append(b.comments[c.Kind], c)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := forEachRow(filepath.Join(dir, "usernames.tsv"), 3, func(f []string) error {
		k := f[0] + "|" + f[1]
		b.userparts[k] = append(b.userparts[k], f[2])
		return nil
	}); err != nil {
		return nil, err
	}
	if err := forEachRow(filepath.Join(dir, "reception.tsv"), 3, func(f []string) error {
		b.Reception = append(b.Reception, ReceptionRow{Title: f[0], Platform: f[1], Tier: f[2]})
		return nil
	}); err != nil {
		return nil, err
	}

	// English is the primary bank; register it, then discover and load any
	// sibling languages (phrases_<lang>.tsv present → load skeletons_<lang>
	// and comments_<lang> too).
	b.lang = "en"
	b.byLang = map[string]*Banks{"en": b}
	langs, err := discoverLanguages(dir)
	if err != nil {
		return nil, err
	}
	for _, lang := range langs {
		lb := &Banks{
			Archetypes: b.Archetypes,
			archByID:   b.archByID,
			userparts:  b.userparts,
			Reception:  b.Reception,
			skeletons:  map[string][]Skeleton{},
			phrases:    map[string][]Phrase{},
			comments:   map[string][]CommentPhrase{},
			lang:       lang,
		}
		if err := loadLangContent(dir, lang, lb); err != nil {
			return nil, err
		}
		b.byLang[lang] = lb
	}
	return b, nil
}

// discoverLanguages returns the non-English language codes with a
// phrases_<lang>.tsv under dir.
func discoverLanguages(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var langs []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "phrases_") && strings.HasSuffix(n, ".tsv") {
			code := strings.TrimSuffix(strings.TrimPrefix(n, "phrases_"), ".tsv")
			if code != "en" && len(code) >= 2 {
				langs = append(langs, code)
			}
		}
	}
	sort.Strings(langs)
	return langs, nil
}

// loadLangContent loads skeletons_<lang>.tsv, phrases_<lang>.tsv and (if
// present) comments_<lang>.tsv into lb. Skeletons and phrases are required;
// a missing comments file just means comment threads on that language's
// reviews fall back to English.
func loadLangContent(dir, lang string, lb *Banks) error {
	if err := forEachRow(filepath.Join(dir, "skeletons_"+lang+".tsv"), 4, func(f []string) error {
		sk := Skeleton{Archetype: f[0], Sentiment: f[1], Weight: atofOr(f[2], 1)}
		for _, tok := range strings.Split(f[3], "|") {
			ref := SlotRef{Name: tok, Prob: 1}
			if i := strings.IndexByte(tok, '?'); i >= 0 {
				ref.Name = tok[:i]
				ref.Prob = atofOr(tok[i+1:], 1)
			}
			sk.Slots = append(sk.Slots, ref)
		}
		lb.skeletons[sk.Archetype+"|"+sk.Sentiment] = append(lb.skeletons[sk.Archetype+"|"+sk.Sentiment], sk)
		return nil
	}); err != nil {
		return err
	}
	if err := forEachRow(filepath.Join(dir, "phrases_"+lang+".tsv"), 6, func(f []string) error {
		p := Phrase{Slot: f[0], Archetype: f[1], Sentiment: f[2],
			EraFrom: atoiOr(f[3], 2005), EraTo: atoiOr(f[4], 2016), Text: f[5],
			Requires: optField(f, 6)}
		lb.phrases[p.Slot] = append(lb.phrases[p.Slot], p)
		return nil
	}); err != nil {
		return err
	}
	// comments optional
	if _, err := os.Stat(filepath.Join(dir, "comments_"+lang+".tsv")); err == nil {
		if err := forEachRow(filepath.Join(dir, "comments_"+lang+".tsv"), 6, func(f []string) error {
			c := CommentPhrase{Kind: f[0], Archetype: f[1], ParentSent: f[2],
				EraFrom: atoiOr(f[3], 2008), EraTo: atoiOr(f[4], 2016), Text: f[5],
				Requires: optField(f, 6)}
			lb.comments[c.Kind] = append(lb.comments[c.Kind], c)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// forEachRow streams a TSV, skipping '#' comment lines and the header row
// (the first non-comment line). Rows shorter than minFields are an error —
// the banks are hand-authored and a short row is a typo worth failing on.
func forEachRow(path string, minFields int, fn func([]string) error) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open bank: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
	header := false
	line := 0
	for sc.Scan() {
		line++
		t := sc.Text()
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if !header {
			header = true // first non-comment line is the column header
			continue
		}
		fields := strings.Split(t, "\t")
		if len(fields) < minFields {
			return fmt.Errorf("%s:%d: %d fields, want >=%d", filepath.Base(path), line, len(fields), minFields)
		}
		if err := fn(fields); err != nil {
			return fmt.Errorf("%s:%d: %w", filepath.Base(path), line, err)
		}
	}
	return sc.Err()
}

// optField returns the trimmed i-th field, or "" if the row is too short —
// for backward-compatible optional trailing columns (e.g. Phrase.Requires).
func optField(f []string, i int) string {
	if i < len(f) {
		return strings.TrimSpace(f[i])
	}
	return ""
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func atofOr(s string, def float64) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return def
	}
	return v
}
