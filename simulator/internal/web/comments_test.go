package web

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCommentEmission(t *testing.T) {
	reviews, e := buildWorld(t, 42)
	reviewByID := make(map[int64]*ReviewRecord, len(reviews))
	for i := range reviews {
		reviewByID[reviews[i].ReviewID] = &reviews[i]
	}

	var comments []CommentRecord
	perReview := map[int64]int{}
	e.EmitComments(reviews, 1, func(c CommentRecord) {
		comments = append(comments, c)
		perReview[c.ReviewID]++
	})
	if len(comments) == 0 {
		t.Fatal("no comments emitted")
	}
	t.Logf("3g: %d reviews → %d comments (%.2f/review)", len(reviews), len(comments),
		float64(len(comments))/float64(len(reviews)))

	commentsLaunch := time.Date(2008, 3, 1, 0, 0, 0, 0, time.UTC)
	dark := time.Date(2016, 9, 30, 23, 59, 59, 0, time.UTC)
	nAccounts := int64(e.AccountCount())
	seen := map[int64]bool{}
	for _, c := range comments {
		if seen[c.CommentID] {
			t.Fatalf("duplicate comment_id %d", c.CommentID)
		}
		seen[c.CommentID] = true
		rev, ok := reviewByID[c.ReviewID]
		if !ok {
			t.Fatalf("comment %d references unknown review %d", c.CommentID, c.ReviewID)
		}
		if c.AccountID < 1 || c.AccountID > nAccounts {
			t.Errorf("comment %d account_id %d out of range", c.CommentID, c.AccountID)
		}
		if c.AccountID == rev.AccountID {
			t.Errorf("comment %d authored by the review author", c.CommentID)
		}
		if strings.TrimSpace(c.Body) == "" || strings.ContainsAny(c.Body, "{}") {
			t.Errorf("comment %d bad body: %q", c.CommentID, c.Body)
		}
		if c.PostedAt.Before(commentsLaunch) || c.PostedAt.After(dark) {
			t.Errorf("comment %d posted %s outside comment era", c.CommentID, c.PostedAt)
		}
		if c.PostedAt.Before(rev.PostedAt) {
			t.Errorf("comment %d predates its review", c.CommentID)
		}
	}

	// Denormalised count must be exact.
	for _, rev := range reviews {
		if rev.CommentCount != perReview[rev.ReviewID] {
			t.Errorf("review %d CommentCount=%d but %d emitted", rev.ReviewID, rev.CommentCount, perReview[rev.ReviewID])
		}
	}

	// At least one hot thread should exist at 3g scale.
	maxThread := 0
	for _, n := range perReview {
		if n > maxThread {
			maxThread = n
		}
	}
	t.Logf("largest thread: %d comments", maxThread)
}

func TestCommentDeterminism(t *testing.T) {
	reviews, e := buildWorld(t, 42)
	run := func() []CommentRecord {
		var cs []CommentRecord
		e.EmitComments(reviews, 1, func(c CommentRecord) { cs = append(cs, c) })
		return cs
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("comment count differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("comment %d differs:\n%+v\n%+v", i, a[i], b[i])
		}
	}
}

// TestThreadSampleSheet appends the biggest thread to REVIEW_SAMPLES-adjacent
// output so the conversation can be read against the design's target thread.
func TestThreadSampleSheet(t *testing.T) {
	reviews, e := buildWorld(t, 42)
	byReview := map[int64][]CommentRecord{}
	e.EmitComments(reviews, 1, func(c CommentRecord) { byReview[c.ReviewID] = append(byReview[c.ReviewID], c) })

	// Pick the review with the most comments.
	var best int64
	bestN := 0
	for rid, cs := range byReview {
		if len(cs) > bestN {
			bestN, best = len(cs), rid
		}
	}
	var rev ReviewRecord
	for _, r := range reviews {
		if r.ReviewID == best {
			rev = r
		}
	}
	title := "the release"
	if rev.ReleaseID != 0 {
		title = cleanDisplayTitle(e.releaseMeta[rev.ReleaseID].Title)
	} else if rev.HardwareID != 0 {
		title = e.hardwareMeta[rev.HardwareID].ModelName
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Thread sample — biggest 3g thread (%d comments)\n\n", bestN)
	stars := strings.Repeat("★", rev.Rating) + strings.Repeat("☆", 5-rev.Rating)
	fmt.Fprintf(&sb, "**%s — %s, %d** `@%s` — *%s*\n> %s\n\n---\n\n",
		stars, rev.Archetype, rev.PostedAt.Year(), e.acctByID(rev.AccountID).username, title, rev.Body)
	shown := 0
	for _, c := range byReview[best] {
		if shown >= 30 {
			fmt.Fprintf(&sb, "\n_(…%d more comments)_\n", bestN-shown)
			break
		}
		fmt.Fprintf(&sb, "- `@%s`: %s\n", e.acctByID(c.AccountID).username, c.Body)
		shown++
	}
	if err := os.WriteFile("THREAD_SAMPLE.md", []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestCommentVarietyMetric(t *testing.T) {
	reviews, e := buildWorld(t, 42)
	var comments []CommentRecord
	e.EmitComments(reviews, 1, func(c CommentRecord) { comments = append(comments, c) })
	if len(comments) == 0 {
		t.Fatal("no comments")
	}
	freq := map[string]int{}
	var totLen int
	for _, c := range comments {
		freq[c.Body]++
		totLen += len(c.Body)
	}
	worst := 0
	for _, n := range freq {
		if n > worst {
			worst = n
		}
	}
	t.Logf("comments=%d distinct=%d (%.0f%%) worst-repeat=%d (%.2f%%) avg_len=%d",
		len(comments), len(freq), 100*float64(len(freq))/float64(len(comments)),
		worst, 100*float64(worst)/float64(len(comments)), totLen/len(comments))
	if pct := float64(worst) / float64(len(comments)); pct > 0.05 {
		t.Errorf("worst comment is %.1f%% of all comments (>5%%)", 100*pct)
	}
}
