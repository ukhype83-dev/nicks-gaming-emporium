// Review construction: turning one captured purchase (or a browsed item)
// into a fully-formed web.reviews row via the grammar engine. The reception
// model sets the score, the sticky archetype sets the voice, era morale
// bends the mood, and the account's real country/city/price are spliced in.
package web

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"emporium/internal/rng"
)

// ReviewRecord is one web.reviews row plus the archetype/sentiment carried
// forward for the comment and vote emitters. comment/helpful/funny counts
// are filled by those later phases.
type ReviewRecord struct {
	ReviewID     int64
	AccountID    int64
	ReleaseID    int64 // 0 → hardware review
	HardwareID   int64 // 0 → software review
	Rating       int
	Title        string
	Body         string
	Language     string
	Verified     bool
	PostedAt     time.Time
	SourceSystem string

	// Denormalised totals, filled by the comment/vote emitters (0 until then).
	CommentCount int
	HelpfulCount int
	FunnyCount   int

	Archetype string // not persisted on the review row; drives comments/votes
	Sentiment string
}

// buildReview constructs a verified review from a captured purchase.
func (e *Emitter) buildReview(rr *rand.Rand, reviewID int64, acct acctRef, arch Archetype, lang string, p Purchase) (ReviewRecord, bool) {
	posted := reviewPostedAt(rr, p.At, acct.created)
	year := posted.Year()
	arch = e.eraAdjustArchetype(rr, arch, year)

	var releaseID, hardwareID int64
	var base float64
	var ctx Context
	switch {
	case p.ReleaseID != 0:
		m, ok := e.releaseMeta[p.ReleaseID]
		if !ok {
			return ReviewRecord{}, false
		}
		releaseID = p.ReleaseID
		base = e.Reception.Score(m.NormTitle, m.Platform, p.ReleaseID)
		ctx = e.releaseContext(rr, m, acct, p, year)
	case p.HardwareID != 0:
		m, ok := e.hardwareMeta[p.HardwareID]
		if !ok {
			return ReviewRecord{}, false
		}
		hardwareID = p.HardwareID
		base = hardwareScore(p.HardwareID)
		ctx = e.hardwareContext(rr, m, acct, p)
	default:
		return ReviewRecord{}, false
	}
	ctx["year"] = itoa(int64(year))

	rating, sent := RateReview(rr, arch, base, year)
	title, body := e.reviewText(rr, arch, rating, sent, year, ctx, lang)
	return ReviewRecord{
		ReviewID: reviewID, AccountID: acct.id,
		ReleaseID: releaseID, HardwareID: hardwareID,
		Rating: rating, Title: title, Body: body,
		Language: lang, Verified: true, PostedAt: posted,
		SourceSystem: webSourceSystem(posted),
		Archetype:    arch.ID, Sentiment: sent,
	}, true
}

// reviewText produces the title/body for a review, or a rating-only review
// (both empty) with archetype-dependent probability. Star-only reviews are
// realistic (most casual reviews carry no prose) and are the biggest single
// lever against text repetition: a review with no body can't repeat one.
func (e *Emitter) reviewText(rr *rand.Rand, arch Archetype, rating int, sent string, year int, ctx Context, lang string) (title, body string) {
	if rr.Float64() < emptyBodyRate(arch.ID) {
		return "", "" // rating-only: just the stars
	}
	rev := e.Banks.forLang(lang).GenerateReview(rr, arch, rating, sent, year, ctx)
	return rev.Title, rev.Body
}

// emptyBodyRate is P(a review is stars-only). Drive-bys mostly don't write;
// essayists always do.
func emptyBodyRate(archID string) float64 {
	switch archID {
	case "driveby":
		return 0.62
	case "dealhunter":
		return 0.34
	case "capslock":
		return 0.28
	case "parent", "superfan":
		return 0.22
	case "grouch", "collector", "troll":
		return 0.16
	case "technerd", "bucks_victim":
		return 0.10
	case "essayist":
		return 0.02
	default:
		return 0.25
	}
}

// buildUnverifiedReview writes about a game the reviewer browsed but did not
// buy — a random catalog release the account holder could plausibly have an
// opinion on. Rare (design §4 ~15%). Chosen deterministically from the
// reception-curated pool (guaranteed to have metadata + a real score).
func (e *Emitter) buildUnverifiedReview(rr *rand.Rand, reviewID int64, acct acctRef, arch Archetype, lang string) (ReviewRecord, bool) {
	if len(e.releaseMeta) == 0 {
		return ReviewRecord{}, false
	}
	// Post anytime in the web-review era, after the account exists.
	lo := CounterLaunch
	if acct.created.After(lo) {
		lo = acct.created
	}
	span := SiteDark.Sub(lo)
	if span <= 0 {
		return ReviewRecord{}, false
	}
	posted := lo.Add(time.Duration(rr.Float64() * float64(span)))
	posted = webMinute(rr, posted)
	year := posted.Year()
	arch = e.eraAdjustArchetype(rr, arch, year)

	rid := e.sampleCuratedReleaseBefore(rr, posted)
	m, ok := e.releaseMeta[rid]
	if !ok {
		return ReviewRecord{}, false
	}
	base := e.Reception.Score(m.NormTitle, m.Platform, rid)
	ctx := e.releaseContext(rr, m, acct, Purchase{Price: 0}, year)
	ctx["year"] = itoa(int64(year))
	rating, sent := RateReview(rr, arch, base, year)
	title, body := e.reviewText(rr, arch, rating, sent, year, ctx, lang)
	return ReviewRecord{
		ReviewID: reviewID, AccountID: acct.id, ReleaseID: rid,
		Rating: rating, Title: title, Body: body,
		Language: lang, Verified: false, PostedAt: posted,
		SourceSystem: webSourceSystem(posted),
		Archetype:    arch.ID, Sentiment: sent,
	}, true
}

// eraAdjustArchetype flips a reviewer into the EmporiumBucks-victim voice for
// a slice of terminal-year (2015-16) reviews — the refund-rage that a normal
// customer expresses only when the store credit trap catches them (anomaly
// A3). Otherwise returns the sticky archetype unchanged.
func (e *Emitter) eraAdjustArchetype(rr *rand.Rand, arch Archetype, year int) Archetype {
	if year >= 2015 && arch.ID != "troll" && rr.Float64() < 0.15 {
		if v, ok := e.Banks.archByID["bucks_victim"]; ok {
			return v
		}
	}
	return arch
}

// reviewPostedAt places a review in time: shortly after a recent purchase,
// or — for a purchase predating The Counter — at some nostalgic later date.
// Never before the account exists, The Counter launched, or after the site
// went dark.
func reviewPostedAt(rr *rand.Rand, purchaseAt, accountCreated time.Time) time.Time {
	floor := CounterLaunch
	if accountCreated.After(floor) {
		floor = accountCreated
	}
	var posted time.Time
	if purchaseAt.Before(CounterLaunch) {
		span := SiteDark.Sub(floor)
		if span <= 0 {
			return webMinute(rr, floor)
		}
		posted = floor.Add(time.Duration(rr.Float64() * float64(span)))
	} else {
		posted = purchaseAt.AddDate(0, 0, 3+rr.IntN(88)) // 3-90 days later
	}
	if posted.Before(floor) {
		posted = floor.AddDate(0, 0, rr.IntN(30))
	}
	if posted.After(SiteDark) {
		posted = SiteDark
	}
	return webMinute(rr, posted)
}

// webMinute stamps a plausible daytime whole-second time (web-log precision).
func webMinute(rr *rand.Rand, d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 8+rr.IntN(15), rr.IntN(60), rr.IntN(60), 0, time.UTC)
}

// retroAgeYears is the game age (review year − release year) at/above which a
// title reads as old/retro, enabling req:retro phrases.
const retroAgeYears = 8

func (e *Emitter) releaseContext(rr *rand.Rand, m ReleaseMeta, acct acctRef, p Purchase, year int) Context {
	genre := orDefault(m.Genre, "game")
	ctx := Context{
		"title":           cleanDisplayTitle(m.Title),
		"platform":        orDefault(m.Platform, "console"),
		"genre":           genre,
		"publisher":       orDefault(m.Publisher, "the publisher"),
		"developer":       orDefault(m.Developer, "the studio"),
		"price":           formatPrice(acct.country, p.Price),
		"city":            acct.city,
		"condition_grade": conditionGrade(p.Condition),
		"n_years":         itoa(int64(8 + rr.IntN(112))), // playtime filler for the {n_years} slot
		"credit_amount":   formatPrice(acct.country, 20+rr.Float64()*90),
	}
	applyReleaseImmersion(ctx, m, year)
	return ctx
}

// applyReleaseImmersion adds the V1.27 immersion fills + gating flags to a
// release review context: the physical {medium}, {release_year}/{game_age}, a
// sibling {other_platform}, and the req:* predicate flags renderSlot gates on.
// It consumes NO RNG (the sibling is chosen by a deterministic index), so it
// leaves the review's draw sequence untouched — the only count shift in V1.27
// comes from the new phrase content itself. year is the review's posted year.
func applyReleaseImmersion(ctx Context, m ReleaseMeta, year int) {
	class, word := classifyMedia(m.Media)
	ctx["medium"] = word
	if class != "" {
		ctx["req:media_"+class] = "1"
	}
	if m.ReleaseYear > 0 {
		ctx["release_year"] = itoa(int64(m.ReleaseYear))
		if age := year - m.ReleaseYear; age > 0 {
			ctx["game_age"] = itoa(int64(age))
			if age >= retroAgeYears {
				ctx["req:retro"] = "1"
			}
		}
	}
	if n := len(m.OtherPlatforms); n > 0 {
		ctx["req:has_other_versions"] = "1"
		idx := (m.ReleaseYear + year) % n // deterministic, RNG-free sibling pick
		if idx < 0 {
			idx = 0
		}
		ctx["other_platform"] = m.OtherPlatforms[idx]
	}
	if m.Developer != "" {
		ctx["req:has_developer"] = "1"
	}
}

// classifyMedia maps a policy.CanonicalMedia string to a coarse media class
// (for req:media_* gating) and a friendly {medium} noun. The class set is small
// — cartridge/card/disc/floppy/cassette — so a phrase can assert cart-only
// ("blow on the pins") or disc-only ("resurfaced") flavour without ever landing
// on the wrong format. An unknown/blank medium yields no class (no
// format-specific phrase fires) and the neutral word "copy". The cases match
// CanonicalMedia's exact outputs (internal/policy/policy.go).
func classifyMedia(media string) (class, word string) {
	switch media {
	case "Cartridge":
		return "cartridge", "cartridge"
	case "HuCard", "DS Game Card", "3DS Game Card", "PlayStation Vita Card":
		return "card", "game card"
	case "Floppy Disk":
		return "floppy", "floppy disk"
	case "Cassette":
		return "cassette", "cassette"
	case "CD-ROM", "GD-ROM", "DVD-ROM", "Blu-ray Disc", "UMD",
		"GameCube Game Disc", "Wii Optical Disc", "Wii U Optical Disc":
		return "disc", "disc"
	default:
		return "", "copy"
	}
}

func (e *Emitter) hardwareContext(rr *rand.Rand, m HardwareMeta, acct acctRef, p Purchase) Context {
	return Context{
		"title":           orDefault(m.ModelName, "the console"),
		"platform":        orDefault(m.Platform, "the system"),
		"genre":           "hardware",
		"publisher":       orDefault(m.Manufacturer, "the manufacturer"),
		"developer":       orDefault(m.Manufacturer, "the manufacturer"),
		"price":           formatPrice(acct.country, p.Price),
		"city":            acct.city,
		"condition_grade": conditionGrade(p.Condition),
		"n_years":         itoa(int64(8 + rr.IntN(112))),
		"credit_amount":   formatPrice(acct.country, 40+rr.Float64()*160),
		// Hardware has no software medium; a neutral word keeps any shared
		// {medium} phrase readable, and no req:media_* flag is set so
		// cartridge/disc-specific lines can never fire on a console review.
		"medium": "unit",
	}
}

// datedRelease pairs a release id with its release date. Pools of these are
// kept sorted ascending by date so a sampler can binary-search the set of
// titles already released at a given moment.
type datedRelease struct {
	id   int64
	date time.Time
}

// sampleReleaseBefore returns a uniformly chosen release id from pool (sorted
// ascending by date) whose release date is on or before `at`; ok is false when
// nothing had shipped yet. One draw — mirrors the catalog availability sampler
// so the web layer never references a game before it existed.
func sampleReleaseBefore(pool []datedRelease, rr *rand.Rand, at time.Time) (int64, bool) {
	cutoff := sort.Search(len(pool), func(i int) bool { return pool[i].date.After(at) })
	if cutoff == 0 {
		return 0, false
	}
	return pool[rr.IntN(cutoff)].id, true
}

// sampleCuratedReleaseBefore returns a reception-curated release (guaranteed
// metadata + score) that had shipped by `at` — used for unverified (browsed)
// reviews so a browsed game is never reviewed before it released. Deterministic
// given rr; 0 when nothing curated had shipped yet (the caller drops the review).
func (e *Emitter) sampleCuratedReleaseBefore(rr *rand.Rand, at time.Time) int64 {
	if len(e.curatedByDate) == 0 {
		e.buildCuratedReleaseIDs()
	}
	id, _ := sampleReleaseBefore(e.curatedByDate, rr, at)
	return id
}

// buildCuratedReleaseIDs collects releases whose normalised title the reception
// index knows — the pool of "famous enough to have an opinion on" games for
// unverified reviews and traffic weighting — sorted by release date. Lazy, once.
func (e *Emitter) buildCuratedReleaseIDs() {
	pool := make([]datedRelease, 0, 4096)
	for id, m := range e.releaseMeta {
		if e.Reception.IsCurated(m.NormTitle, m.Platform) {
			pool = append(pool, datedRelease{id: id, date: m.ReleaseDate})
		}
	}
	// Sort by date, then id — a total order independent of map iteration.
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].date.Equal(pool[j].date) {
			return pool[i].id < pool[j].id
		}
		return pool[i].date.Before(pool[j].date)
	})
	e.curatedByDate = pool
}

// pickArchetype selects a reviewer's sticky voice, tilting toward the
// forum-rat archetypes (WhaleMult) as their purchase count climbs. Excludes
// bucks_victim (a situational mode, injected by eraAdjustArchetype).
func (e *Emitter) pickArchetype(cid int64, purchaseCount int) Archetype {
	tilt := 0
	if purchaseCount > 5 {
		tilt = (purchaseCount - 5) / 5
		if tilt > 6 {
			tilt = 6
		}
	}
	return e.pickArchetypeKeyed("web/archetype/"+itoa(cid), tilt)
}

// pickArchetypeKeyed is the weighted archetype draw, keyed by an arbitrary
// namespace (reviewers key on customer, commenters on account). tilt scales
// the whale archetypes up.
func (e *Emitter) pickArchetypeKeyed(key string, tilt int) Archetype {
	r := rng.Derive(e.seed, key)
	weights := make([]float64, len(e.Banks.Archetypes))
	total := 0.0
	for i, a := range e.Banks.Archetypes {
		if a.ID == "bucks_victim" {
			continue // situational, not sticky
		}
		w := a.Weight
		if tilt > 0 {
			w *= 1 + (a.WhaleMult-1)*float64(tilt)/6
		}
		weights[i] = w
		total += w
	}
	roll := r.Float64() * total
	for i, a := range e.Banks.Archetypes {
		if weights[i] == 0 {
			continue
		}
		roll -= weights[i]
		if roll <= 0 {
			return a
		}
	}
	return e.Banks.archByID["driveby"]
}

// perPurchaseReviewProb: how often a reviewer of this archetype bothers to
// review any given purchase. Drive-bys spray, essayists are selective.
func perPurchaseReviewProb(archID string) float64 {
	switch archID {
	case "driveby":
		return 0.50
	case "troll":
		return 0.45
	case "superfan":
		return 0.42
	case "grouch":
		return 0.32
	case "essayist":
		return 0.16
	case "collector", "technerd":
		return 0.26
	default:
		return 0.28
	}
}

// unverifiedCount: browsed-not-bought reviews. Trolls and superfans opine on
// things they never bought; most people don't.
func unverifiedCount(rr *rand.Rand, archID string) int {
	p := 0.10
	switch archID {
	case "troll":
		p = 0.55
	case "superfan", "grouch":
		p = 0.22
	}
	n := 0
	for n < 3 && rr.Float64() < p {
		n++
		p *= 0.4
	}
	return n
}

// languageFor keys off the account holder's country, with a realistic share
// of non-English-speaking countries reviewing in English anyway. Tranche 1
// only has an English bank, so non-English codes are skipped upstream.
func languageFor(country string, rr *rand.Rand) string {
	switch country {
	case "US", "GB", "CA", "AU", "IE", "NZ":
		return "en"
	}
	native, englishAnyway := nativeLanguage(country)
	if rr.Float64() < englishAnyway {
		return "en"
	}
	return native
}

func nativeLanguage(country string) (string, float64) {
	switch country {
	case "DE", "AT":
		return "de", 0.15
	case "CH":
		return "de", 0.30
	case "FR":
		return "fr", 0.12
	case "JP":
		return "ja", 0.03
	case "BR":
		return "pt", 0.10
	case "PL":
		return "pl", 0.10
	case "SE":
		return "sv", 0.55
	case "NL":
		return "nl", 0.60
	case "DK":
		return "da", 0.60
	case "NO":
		return "no", 0.55
	case "KR":
		return "ko", 0.05
	case "ES":
		return "es", 0.15
	case "IT":
		return "it", 0.12
	case "CZ":
		return "cs", 0.10
	default:
		return "en", 1.0
	}
}

// --- small formatting helpers ---------------------------------------------

func hardwareScore(hardwareID int64) float64 {
	// Consoles are generally liked; spread [3.3, 4.4] deterministically.
	return 3.3 + (hashPrior(hardwareID)-2.6)/1.8*1.1
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// cleanDisplayTitle trims a raw catalog title's multi-region denorm tail.
func cleanDisplayTitle(raw string) string {
	for _, sep := range []string{" •", " / ", "  "} {
		if i := strings.Index(raw, sep); i > 0 {
			raw = raw[:i]
		}
	}
	return strings.TrimSpace(raw)
}

func conditionGrade(cond string) string {
	switch cond {
	case "", "new":
		return "new"
	case "used_mint":
		return "Used - Like New"
	case "used_good":
		return "Used - Good"
	case "used_fair":
		return "Used - Fair"
	case "used_loose":
		return "Used - Loose"
	default:
		return "Used - Good"
	}
}

func formatPrice(country string, amount float64) string {
	if amount <= 0 {
		amount = 0
	}
	sym, before := currencySymbol(country)
	if before {
		return fmt.Sprintf("%s%.2f", sym, amount)
	}
	return fmt.Sprintf("%.2f%s", amount, sym)
}

func currencySymbol(country string) (string, bool) {
	switch country {
	case "GB":
		return "£", true
	case "JP":
		return "¥", true
	case "DE", "FR", "ES", "IT", "NL", "IE", "AT":
		return "€", true
	case "SE", "NO", "DK":
		return " kr", false
	case "PL":
		return " zł", false
	case "BR":
		return "R$", true
	case "CH":
		return "CHF ", true
	default: // US, CA, AU, NZ, KR (approx), fallback
		return "$", true
	}
}
