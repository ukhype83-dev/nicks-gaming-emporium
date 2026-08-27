// The grammar assembler: skeleton → slots → fragments → quirk pass.
// Fallback matching is deliberately forgiving (relax sentiment, then era,
// then archetype) so a thin bank degrades to blander prose instead of
// dead-ending — bank gaps show up in review QUALITY at the editorial gate,
// never as build failures.
package web

import (
	"math/rand/v2"
	"strconv"
	"strings"
)

// Context carries the real-data fills spliced into fragments. Keys are the
// micro-slot names used in the banks: title, platform, genre, publisher,
// developer, price, city, n_years, credit_amount, year, month_name,
// condition_grade, prev_user, parent_user. Missing keys render as "" and the
// engine tidies the whitespace — fragments should be authored so any data
// micro-slot sits mid-sentence only when the caller can fill it.
type Context map[string]string

// Review is one assembled review body (rating/sentiment decided by the
// reception model in sentiment.go before assembly).
type Review struct {
	Rating    int
	Sentiment string // pos|mixed|neg
	Title     string
	Body      string
}

const maxSlotDepth = 4

// GenerateReview assembles a review for the archetype at the given rating/
// sentiment and year. r must be a per-review derived stream
// ("web/review/<id>"). The rating is injected as the {stars} micro-slot so
// fragments that state a count always agree with the stars on the page.
// Fragments never repeat within one review: a duplicate draw is re-rolled
// once, then dropped (an author writing the same sentence twice is a bank
// gap the editorial gate should see as blandness, not as a stutter).
func (b *Banks) GenerateReview(r *rand.Rand, arch Archetype, rating int, sentiment string, year int, ctx Context) Review {
	if _, ok := ctx["stars"]; !ok {
		c := make(Context, len(ctx)+1)
		for k, v := range ctx {
			c[k] = v
		}
		c["stars"] = strconv.Itoa(rating)
		ctx = c
	}
	sk := b.pickSkeleton(r, arch.ID, sentiment)
	used := map[string]bool{}
	var parts []string
	for _, ref := range sk.Slots {
		if ref.Prob < 1 && r.Float64() >= ref.Prob {
			continue
		}
		s := b.renderSlot(r, ref.Name, arch.ID, sentiment, year, ctx, 0)
		if s != "" && used[s] {
			s = b.renderSlot(r, ref.Name, arch.ID, sentiment, year, ctx, 0) // one re-roll
		}
		if s != "" && !used[s] {
			used[s] = true
			parts = append(parts, s)
		}
	}
	body := b.applyQuirks(r, arch, strings.Join(parts, " "))

	rev := Review{Rating: rating, Sentiment: sentiment, Body: body}
	if r.Float64() < arch.TitleRate {
		rev.Title = b.generateTitle(r, sk, arch, sentiment, year, ctx)
	}
	return rev
}

// generateTitle renders the skeleton's FIRST slot as an independent draw —
// the classic self-titling reviewer ("I wanted to like this."). Truncated at
// the first sentence end, then at a word boundary. Tranche 2 may add
// dedicated title_* slots (design Appendix B follow-up).
func (b *Banks) generateTitle(r *rand.Rand, sk Skeleton, arch Archetype, sentiment string, year int, ctx Context) string {
	t := b.renderSlot(r, sk.Slots[0].Name, arch.ID, sentiment, year, ctx, 0)
	if i := strings.IndexAny(t, ".!?"); i > 0 && i < len(t)-1 {
		t = t[:i+1]
	}
	if len(t) > 60 {
		if sp := strings.LastIndexByte(t[:60], ' '); sp > 20 {
			t = t[:sp]
		} else {
			t = t[:60]
		}
	}
	return strings.TrimSpace(t)
}

func (b *Banks) pickSkeleton(r *rand.Rand, archID, sentiment string) Skeleton {
	cands := b.skeletons[archID+"|"+sentiment]
	if len(cands) == 0 {
		// Archetype fallback: non-English banks are archetype-agnostic
		// ("any" skeletons), so a superfan|pos lookup borrows any|pos.
		cands = b.skeletons["any|"+sentiment]
	}
	if len(cands) == 0 {
		// Sentiment fallback: an archetype with no neg skeleton (e.g. a
		// pos-only voice) borrows its mixed plan rather than failing.
		for _, s := range []string{"mixed", "pos", "neg"} {
			if cs := b.skeletons[archID+"|"+s]; len(cs) > 0 {
				cands = cs
				break
			}
			if cs := b.skeletons["any|"+s]; len(cs) > 0 {
				cands = cs
				break
			}
		}
	}
	total := 0.0
	for _, s := range cands {
		total += s.Weight
	}
	roll := r.Float64() * total
	for _, s := range cands {
		roll -= s.Weight
		if roll <= 0 {
			return s
		}
	}
	return cands[len(cands)-1]
}

// renderSlot picks a fragment for slot and expands its micro-slots. The
// candidate filter relaxes in stages so authored gaps degrade gracefully:
//  1. archetype (exact|any) + sentiment (exact|any) + era
//  2. drop the sentiment requirement
//  3. drop the era requirement
//  4. any row for the slot
func (b *Banks) renderSlot(r *rand.Rand, slot, archID, sentiment string, year int, ctx Context, depth int) string {
	if depth > maxSlotDepth {
		return ""
	}
	rows := b.phrases[slot]
	if len(rows) == 0 {
		return ""
	}
	pick := func(match func(Phrase) bool) []Phrase {
		var out []Phrase
		exactArch := false
		for _, p := range rows {
			if !match(p) {
				continue
			}
			// Archetype-exact rows beat 'any' rows: once one exact row
			// matches, 'any' rows are dropped so voices stay distinct.
			if p.Archetype == archID && !exactArch {
				exactArch = true
				out = out[:0]
			}
			if exactArch && p.Archetype != archID {
				continue
			}
			out = append(out, p)
		}
		return out
	}
	archOK := func(p Phrase) bool { return p.Archetype == archID || p.Archetype == "any" }
	eraOK := func(p Phrase) bool { return year >= p.EraFrom && year <= p.EraTo }
	sentOK := func(p Phrase) bool { return p.Sentiment == sentiment || p.Sentiment == "any" }
	// reqOK is a HARD gate applied at EVERY rung of the relaxation ladder
	// (V1.27): a phrase whose `requires` predicate the context fails is never
	// eligible, so a cart-only line can't land on a disc game even in the final
	// fallback. Rows with no `requires` always pass, so existing prose is
	// unchanged. If nothing satisfies requires, the slot renders "" (drop it)
	// rather than assert something false.
	reqOK := func(p Phrase) bool { return requiresSatisfied(p.Requires, ctx) }

	cands := pick(func(p Phrase) bool { return archOK(p) && sentOK(p) && eraOK(p) && reqOK(p) })
	if len(cands) == 0 {
		cands = pick(func(p Phrase) bool { return archOK(p) && eraOK(p) && reqOK(p) })
	}
	if len(cands) == 0 {
		cands = pick(func(p Phrase) bool { return archOK(p) && reqOK(p) })
	}
	if len(cands) == 0 {
		cands = pick(reqOK)
	}
	if len(cands) == 0 {
		return ""
	}
	text := cands[r.IntN(len(cands))].Text
	return b.expand(r, text, archID, sentiment, year, ctx, depth)
}

// expand fills {micro-slots}: context keys first, then recursive slot
// lookups, else the braces vanish. Whitespace is tidied afterwards.
func (b *Banks) expand(r *rand.Rand, text, archID, sentiment string, year int, ctx Context, depth int) string {
	var sb strings.Builder
	for {
		i := strings.IndexByte(text, '{')
		if i < 0 {
			sb.WriteString(text)
			break
		}
		j := strings.IndexByte(text[i:], '}')
		if j < 0 {
			sb.WriteString(text)
			break
		}
		sb.WriteString(text[:i])
		name := text[i+1 : i+j]
		if v, ok := ctx[name]; ok {
			sb.WriteString(v)
		} else if len(b.phrases[name]) > 0 {
			sb.WriteString(b.renderSlot(r, name, archID, sentiment, year, ctx, depth+1))
		}
		text = text[i+j+1:]
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}

// GenerateComment picks a conversational move against a parent review.
// kind is one of the comments_en.tsv kinds; parentSent the parent review's
// sentiment. Same relaxation ladder as renderSlot.
func (b *Banks) GenerateComment(r *rand.Rand, kind string, arch Archetype, parentSent string, year int, ctx Context) string {
	rows := b.comments[kind]
	if len(rows) == 0 {
		return ""
	}
	filter := func(needSent, needEra bool) []CommentPhrase {
		var out []CommentPhrase
		exact := false
		for _, c := range rows {
			if c.Archetype != arch.ID && c.Archetype != "any" {
				continue
			}
			if !requiresSatisfied(c.Requires, ctx) { // V1.27 hard gate
				continue
			}
			if needSent && c.ParentSent != parentSent && c.ParentSent != "any" {
				continue
			}
			if needEra && (year < c.EraFrom || year > c.EraTo) {
				continue
			}
			if c.Archetype == arch.ID && !exact {
				exact = true
				out = out[:0]
			}
			if exact && c.Archetype != arch.ID {
				continue
			}
			out = append(out, c)
		}
		return out
	}
	cands := filter(true, true)
	if len(cands) == 0 {
		cands = filter(false, true)
	}
	if len(cands) == 0 {
		cands = filter(false, false)
	}
	if len(cands) == 0 {
		return ""
	}
	text := b.expand(r, cands[r.IntN(len(cands))].Text, arch.ID, parentSent, year, ctx, 0)
	return b.applyQuirks(r, arch, text)
}

// applyQuirks adds the archetype's noise AFTER assembly (design: fragments
// are authored clean except where the voice itself demands otherwise).
// Typos: per-word chance to swap two adjacent letters or drop an apostrophe.
// Caps: per-word chance to SHOUT a word (on top of authored caps).
func (b *Banks) applyQuirks(r *rand.Rand, arch Archetype, s string) string {
	typoRate, capsRate := arch.TypoRate, arch.CapsRate
	// Non-English banks are hand-authored and lighter — keep the mangling
	// minimal so the text stays clean (a rare human typo, no shouting).
	if b.lang != "" && b.lang != "en" {
		typoRate *= 0.25
		capsRate = 0
	}
	if typoRate == 0 && capsRate == 0 {
		return s
	}
	words := strings.Split(s, " ")
	for i, w := range words {
		// Never corrupt an @mention (the handle must stay joinable) or a
		// self-censored swear token (V1.27 {censored}: the *'s must survive).
		if strings.HasPrefix(w, "@") || strings.IndexByte(w, '*') >= 0 {
			continue
		}
		if len(w) > 3 && r.Float64() < typoRate {
			if strings.Contains(w, "'") && r.Float64() < 0.5 {
				words[i] = strings.Replace(w, "'", "", 1)
			} else {
				p := 1 + r.IntN(len(w)-2)
				bs := []byte(w)
				if isLower(bs[p]) && isLower(bs[p+1]) {
					bs[p], bs[p+1] = bs[p+1], bs[p]
					words[i] = string(bs)
				}
			}
		}
		if len(w) > 3 && r.Float64() < capsRate*0.12 {
			words[i] = strings.ToUpper(words[i])
		}
	}
	return strings.Join(words, " ")
}

func isLower(b byte) bool { return b >= 'a' && b <= 'z' }

// requiresSatisfied evaluates a phrase's `requires` predicate list against the
// review context (V1.27). requires is a comma-separated AND of tokens; a token
// is satisfied when the context carries the flag key "req:<token>" (any value),
// which releaseContext sets for the resolved medium class, other-versions and
// retro age. A leading "!" negates a token. Empty requires is always satisfied
// (so pre-V1.27 rows and every non-gated phrase are unaffected). Flags are read
// here only — they are never rendered as {placeholders}.
func requiresSatisfied(requires string, ctx Context) bool {
	if requires == "" {
		return true
	}
	for _, tok := range strings.Split(requires, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		neg := false
		if strings.HasPrefix(tok, "!") {
			neg = true
			tok = tok[1:]
		}
		_, present := ctx["req:"+tok]
		if present == neg {
			return false
		}
	}
	return true
}
