package web

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"emporium/internal/catalog"
	"emporium/internal/customers"
	"emporium/internal/geography"
	"emporium/internal/hardware"
	"emporium/internal/policy"
	"emporium/internal/shops"
	"emporium/internal/transactions"
)

const (
	catalogPath  = "../../../seed_data/releases.tsv"
	hardwarePath = "../../../seed_data/hardware.tsv"
)

// buildWorld runs a full 3g replay — customers → accounts → reviewers →
// transaction capture → reviews — and returns the review records. This is
// the real integration path (in-process, no DB), the same sequence
// load_web.go will drive.
func buildWorld(t *testing.T, seed uint64) ([]ReviewRecord, *Emitter) {
	t.Helper()
	asOf := time.Date(2025, 4, 23, 0, 0, 0, 0, time.UTC)

	postals, err := geography.Load(postalPath)
	if err != nil {
		t.Fatalf("postals: %v", err)
	}
	cat, err := catalog.Load(catalogPath)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	hw, err := hardware.Load(hardwarePath)
	if err != nil {
		t.Fatalf("hardware: %v", err)
	}
	banks := loadBanksT(t)

	e := NewEmitter(banks, seed, asOf, 1)

	// 1. Accounts (customer replay).
	_, custIndex, err := customers.Generate("3g", seed, asOf, postals, func(c customers.Customer) {
		e.AccountFor(c)
	})
	if err != nil {
		t.Fatalf("customers.Generate: %v", err)
	}
	// 2. Reviewers + catalog lookups.
	e.MarkReviewers()
	e.SetCatalogs(releaseMetaFrom(cat), hardwareMetaFrom(hw))

	// 3. Capture purchases via a transaction replay (nil staff is safe —
	//    staff RNG is an independent stream; customer linkage is identical).
	shopList, err := shops.Generate("3g", seed, asOf, postals)
	if err != nil {
		t.Fatalf("shops: %v", err)
	}
	_, err = transactionsGenerate(seed, asOf, shopList, cat, hw, custIndex, e)
	if err != nil {
		t.Fatalf("tx replay: %v", err)
	}

	// 4. Reviews.
	var reviews []ReviewRecord
	e.EmitReviews(1, func(r ReviewRecord) { reviews = append(reviews, r) })
	return reviews, e
}

func TestReviewEmissionIntegration(t *testing.T) {
	reviews, e := buildWorld(t, 42)
	if len(reviews) == 0 {
		t.Fatal("no reviews emitted")
	}
	t.Logf("3g: %d accounts, %d reviewers, %d reviews (%.1f/reviewer)",
		e.AccountCount(), e.ReviewerCount(), len(reviews),
		float64(len(reviews))/float64(max1(e.ReviewerCount())))

	counter := time.Date(2005, 11, 1, 0, 0, 0, 0, time.UTC)
	dark := time.Date(2016, 9, 30, 23, 59, 59, 0, time.UTC)
	verified, seenID := 0, map[int64]bool{}
	for _, r := range reviews {
		if seenID[r.ReviewID] {
			t.Fatalf("duplicate review_id %d", r.ReviewID)
		}
		seenID[r.ReviewID] = true
		if r.Rating < 1 || r.Rating > 5 {
			t.Errorf("review %d rating %d out of range", r.ReviewID, r.Rating)
		}
		// Empty body is valid — a rating-only (stars-only) review. A body
		// that IS present must be fully expanded (no leaked slots).
		if r.Body == "" && r.Title != "" {
			t.Errorf("review %d has a title but no body", r.ReviewID)
		}
		if strings.ContainsAny(r.Body+r.Title, "{}") {
			t.Errorf("review %d has an unexpanded slot: %q / %q", r.ReviewID, r.Title, r.Body)
		}
		if (r.ReleaseID == 0) == (r.HardwareID == 0) {
			t.Errorf("review %d must target exactly one of release/hardware", r.ReviewID)
		}
		if r.PostedAt.Before(counter) || r.PostedAt.After(dark) {
			t.Errorf("review %d posted %s outside the Counter era", r.ReviewID, r.PostedAt)
		}
		// Every emitted review's language must have a loaded bank (the
		// emitter gates on HasLanguage).
		if !e.Banks.HasLanguage(r.Language) {
			t.Errorf("review %d emitted in language %q with no bank", r.ReviewID, r.Language)
		}
		if r.Verified {
			verified++
		}
	}
	// Most reviews are verified purchases (design ~85%).
	if frac := float64(verified) / float64(len(reviews)); frac < 0.65 {
		t.Errorf("verified share %.2f unexpectedly low", frac)
	}
}

func TestReviewDeterminism(t *testing.T) {
	r1, _ := buildWorld(t, 42)
	r2, _ := buildWorld(t, 42)
	if len(r1) != len(r2) {
		t.Fatalf("review count differs: %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("review %d differs:\n%+v\n%+v", i, r1[i], r2[i])
		}
	}
}

// TestReviewSampleSheet writes REVIEW_SAMPLES.md — real generated reviews
// against the real 3g catalog, the editorial-gate #2 artifact.
func TestReviewSampleSheet(t *testing.T) {
	reviews, e := buildWorld(t, 42)
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Review samples — real 3g generation\n")
	fmt.Fprintf(&sb, "<!-- %d accounts, %d reviewers, %d reviews. Deterministic at seed 42. -->\n",
		e.AccountCount(), e.ReviewerCount(), len(reviews))
	fmt.Fprintf(&sb, "<!-- Editorial gate #2: read against NGE_V1.26_WEB_DESIGN.md Appendix A. -->\n\n")

	// A spread across the years and a mix of verified/unverified.
	shown, byYear := 0, map[int]int{}
	for _, r := range reviews {
		yr := r.PostedAt.Year()
		if byYear[yr] >= 6 || shown >= 60 {
			continue
		}
		byYear[yr]++
		shown++
		title := "the release"
		if r.ReleaseID != 0 {
			title = cleanDisplayTitle(e.releaseMeta[r.ReleaseID].Title)
		} else if r.HardwareID != 0 {
			title = e.hardwareMeta[r.HardwareID].ModelName
		}
		stars := strings.Repeat("★", r.Rating) + strings.Repeat("☆", 5-r.Rating)
		vflag := ""
		if r.Verified {
			vflag = ", verified"
		}
		fmt.Fprintf(&sb, "**%s — %s, %d%s** — *%s*\n", stars, r.Archetype, yr, vflag, title)
		if r.Title != "" {
			fmt.Fprintf(&sb, "Title: %q\n", r.Title)
		}
		fmt.Fprintf(&sb, "> %s\n\n", r.Body)
	}
	if err := os.WriteFile("REVIEW_SAMPLES.md", []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write samples: %v", err)
	}
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// releaseMetaFrom mirrors dbwriter.buildReleaseMeta so the in-process test
// world exercises the V1.27 immersion plumbing (medium/other-platforms/year)
// exactly as the DB load does.
func releaseMetaFrom(cat *catalog.Index) map[int64]ReleaseMeta {
	all := cat.All()
	platformsByTitle := make(map[string]map[string]bool, len(all))
	for _, r := range all {
		nt := strings.ToLower(strings.TrimSpace(r.Title))
		if platformsByTitle[nt] == nil {
			platformsByTitle[nt] = map[string]bool{}
		}
		if r.Platform != "" {
			platformsByTitle[nt][r.Platform] = true
		}
	}
	m := make(map[int64]ReleaseMeta, len(all))
	for _, r := range all {
		nt := strings.ToLower(strings.TrimSpace(r.Title))
		year := 0
		if !r.ReleaseDate.IsZero() {
			year = r.ReleaseDate.Year()
		}
		var others []string
		for p := range platformsByTitle[nt] {
			if p != r.Platform {
				others = append(others, p)
			}
		}
		sort.Strings(others)
		m[r.ReleaseID] = ReleaseMeta{
			NormTitle:      nt,
			Title:          r.Title,
			Platform:       r.Platform,
			Genre:          r.Genre,
			Publisher:      r.Publisher,
			Developer:      r.Developer,
			ReleaseYear:    year,
			Media:          policy.CanonicalMedia(r.Platform, year),
			OtherPlatforms: others,
		}
	}
	return m
}

func hardwareMetaFrom(hw *hardware.Index) map[int64]HardwareMeta {
	m := make(map[int64]HardwareMeta, hw.Count())
	for _, h := range hw.All() {
		m[h.HardwareID] = HardwareMeta{
			ModelName:    h.ModelName,
			Platform:     h.Platform,
			Kind:         h.Kind,
			Manufacturer: h.Manufacturer,
		}
	}
	return m
}

// transactionsGenerate replays the transaction stream, feeding every forward
// sale line into the emitter's purchase capture (nil staff: the staff RNG is
// an independent stream, so customer linkage is identical to the real load).
func transactionsGenerate(seed uint64, asOf time.Time, shopList []shops.Shop, cat *catalog.Index, hw *hardware.Index, cust transactions.CustomerPicker, e *Emitter) (int64, error) {
	return transactions.Generate("3g", seed, asOf, shopList, cat, hw, cust, nil, func(tx transactions.Transaction) {
		if tx.CustomerID == nil || tx.OriginalTransactionID != nil {
			return
		}
		at := parseCustDate(tx.OccurredAt)
		for _, ln := range tx.Lines {
			if ln.ReleaseID == 0 && ln.HardwareID == 0 {
				continue
			}
			e.CaptureSale(*tx.CustomerID, ln.ReleaseID, ln.HardwareID, at, ln.Condition, ln.LineTotal)
		}
	})
}

func TestReviewVarietyMetric(t *testing.T) {
	reviews, _ := buildWorld(t, 42)
	empty := 0
	firstSent := map[string]int{}
	fullBody := map[string]int{}
	for _, r := range reviews {
		if r.Body == "" {
			empty++
			continue
		}
		fullBody[r.Body]++
		s := r.Body
		if i := strings.IndexAny(s, ".!?"); i > 0 {
			s = s[:i]
		}
		firstSent[s]++
	}
	topSent, topSentN := "", 0
	for s, n := range firstSent {
		if n > topSentN {
			topSentN, topSent = n, s
		}
	}
	topBodyN := 0
	for _, n := range fullBody {
		if n > topBodyN {
			topBodyN = n
		}
	}
	textReviews := len(reviews) - empty
	t.Logf("reviews=%d empty(rating-only)=%d (%.0f%%) text=%d distinct_openers=%d distinct_bodies=%d",
		len(reviews), empty, 100*float64(empty)/float64(len(reviews)), textReviews, len(firstSent), len(fullBody))
	t.Logf("worst-repeated opener: %dx (%.1f%% of text reviews) — %q", topSentN, 100*float64(topSentN)/float64(textReviews), topSent)
	t.Logf("worst-repeated full body: %dx", topBodyN)

	// Variety guards (protect against bank shrinkage regressing repetition).
	if frac := float64(empty) / float64(len(reviews)); frac < 0.15 || frac > 0.40 {
		t.Errorf("rating-only share %.2f outside 0.15-0.40", frac)
	}
	if len(firstSent) < 300 {
		t.Errorf("only %d distinct openers — bank too thin", len(firstSent))
	}
	if worst := float64(topSentN) / float64(textReviews); worst > 0.05 {
		t.Errorf("worst opener is %.1f%% of text reviews (>5%%) — too repetitive", 100*worst)
	}
}

func TestMultilangSamples(t *testing.T) {
	reviews, e := buildWorld(t, 42)
	byLang := map[string]int{}
	shown := map[string]int{}
	var sb strings.Builder
	sb.WriteString("# Non-English review samples (real 3g)\n\n")
	for _, r := range reviews {
		byLang[r.Language]++
		if r.Language == "en" || r.Body == "" || shown[r.Language] >= 3 {
			continue
		}
		shown[r.Language]++
		title := "?"
		if r.ReleaseID != 0 {
			title = cleanDisplayTitle(e.releaseMeta[r.ReleaseID].Title)
		}
		stars := strings.Repeat("★", r.Rating) + strings.Repeat("☆", 5-r.Rating)
		fmt.Fprintf(&sb, "**[%s] %s** — *%s*\n> %s\n\n", r.Language, stars, title, r.Body)
	}
	// distribution line
	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	for _, l := range langs {
		t.Logf("lang %s: %d reviews (%.1f%%)", l, byLang[l], 100*float64(byLang[l])/float64(len(reviews)))
	}
	os.WriteFile("MULTILANG_SAMPLES.md", []byte(sb.String()), 0o644)
}
