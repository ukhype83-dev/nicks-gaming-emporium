package web

import (
	"testing"
	"time"
)

func TestVoteEmission(t *testing.T) {
	reviews, e := buildWorld(t, 42)
	// Comments first (votesFor reads CommentCount).
	e.EmitComments(reviews, 1, func(CommentRecord) {})

	reviewByID := make(map[int64]*ReviewRecord, len(reviews))
	for i := range reviews {
		reviewByID[reviews[i].ReviewID] = &reviews[i]
	}
	helpful, funny := map[int64]int{}, map[int64]int{}
	var total int
	dupGuard := map[[2]int64]map[string]bool{} // (review,account) → types seen

	e.EmitVotes(reviews, 1, func(v VoteRecord) {
		total++
		rev, ok := reviewByID[v.ReviewID]
		if !ok {
			t.Fatalf("vote %d → unknown review %d", v.VoteID, v.ReviewID)
		}
		if v.AccountID == rev.AccountID {
			t.Errorf("vote %d by the review author", v.VoteID)
		}
		switch v.VoteType {
		case "helpful":
			helpful[v.ReviewID]++
		case "funny":
			funny[v.ReviewID]++
		case "unhelpful":
		default:
			t.Errorf("vote %d bad type %q", v.VoteID, v.VoteType)
		}
		if v.OccurredAt.Before(rev.PostedAt) {
			t.Errorf("vote %d predates its review", v.VoteID)
		}
		key := [2]int64{v.ReviewID, v.AccountID}
		if dupGuard[key] == nil {
			dupGuard[key] = map[string]bool{}
		}
		if dupGuard[key][v.VoteType] {
			t.Errorf("duplicate vote: review %d account %d type %s", v.ReviewID, v.AccountID, v.VoteType)
		}
		dupGuard[key][v.VoteType] = true
	})
	if total == 0 {
		t.Fatal("no votes emitted")
	}
	t.Logf("3g: %d reviews → %d votes (%.1f/review)", len(reviews), total, float64(total)/float64(len(reviews)))

	// Denormalised helpful/funny must be exact.
	for _, rev := range reviews {
		if rev.HelpfulCount != helpful[rev.ReviewID] {
			t.Errorf("review %d HelpfulCount=%d, emitted %d", rev.ReviewID, rev.HelpfulCount, helpful[rev.ReviewID])
		}
		if rev.FunnyCount != funny[rev.ReviewID] {
			t.Errorf("review %d FunnyCount=%d, emitted %d", rev.ReviewID, rev.FunnyCount, funny[rev.ReviewID])
		}
	}
}

func TestClickstreamEmission(t *testing.T) {
	_, e := buildWorld(t, 42)
	nAccounts := int64(e.AccountCount())

	var n int
	start := time.Date(2004, 1, 1, 0, 0, 0, 0, time.UTC)
	dark := time.Date(2016, 9, 30, 23, 59, 59, 0, time.UTC)
	migration := time.Date(2007, 10, 1, 0, 0, 0, 0, time.UTC)
	seenSession := map[int64]bool{}
	var botHits, loggedIn, preMigrationRows int

	// Small scale keeps the test quick; the loader uses the real tier scale.
	e.EmitClickstream(0.00006, 1, 1, func(v PageViewRecord) {
		n++
		if v.OccurredAt.Before(start) || v.OccurredAt.After(dark) {
			t.Errorf("page view %d at %s outside web era", v.PageViewID, v.OccurredAt)
		}
		if v.OccurredAt.Before(clickstreamStart) {
			t.Errorf("page view %d survived before the survival window", v.PageViewID)
		}
		if v.AccountID != 0 {
			if v.AccountID < 1 || v.AccountID > nAccounts {
				t.Errorf("page view %d account_id %d out of range", v.PageViewID, v.AccountID)
			}
			loggedIn++
		}
		if v.URLPath == "" || v.ClientCountry == "" || v.UserAgentFamily == "" {
			t.Errorf("page view %d missing required field", v.PageViewID)
		}
		if v.HTTPStatus < 200 || v.HTTPStatus > 599 {
			t.Errorf("page view %d bad status %d", v.PageViewID, v.HTTPStatus)
		}
		if v.UserAgentFamily == "Googlebot" || v.UserAgentFamily == "bingbot" {
			botHits++
		}
		if v.OccurredAt.Before(migration) {
			preMigrationRows++
		}
		seenSession[v.SessionID] = true
	})
	if n == 0 {
		t.Fatal("no page views emitted")
	}
	t.Logf("3g clickstream (scale 6e-5): %d page views, %d sessions, %d bot-UA, %d logged-in, %d pre-migration",
		n, len(seenSession), botHits, loggedIn, preMigrationRows)
	if botHits == 0 {
		t.Error("no crawler traffic at all")
	}
	if preMigrationRows == 0 {
		t.Error("no surviving pre-migration (sampled) traffic")
	}
}

func TestClickstreamDeterminism(t *testing.T) {
	_, e := buildWorld(t, 42)
	run := func() []PageViewRecord {
		var vs []PageViewRecord
		e.EmitClickstream(0.00002, 1, 1, func(v PageViewRecord) { vs = append(vs, v) })
		return vs
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("page view count differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("page view %d differs:\n%+v\n%+v", i, a[i], b[i])
		}
	}
}
