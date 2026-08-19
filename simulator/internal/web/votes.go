// Review votes (design §4) — the Votes analog. Each vote is helpful, funny
// or unhelpful. Useful voices (grouch, tech-nerd, essayist) earn helpful;
// loud ones (caps-lock, troll, drive-by) earn funny; the fake pockets (bombs,
// shills) collect unhelpful from the regulars. helpful/funny are the shown,
// denormalised counts on the review; unhelpful lives only in the vote table —
// the quiet signal the anomaly labs use to unmask astroturf.
package web

import (
	"math/rand/v2"
	"time"

	"emporium/internal/rng"
)

// VoteRecord is one web.review_votes row.
type VoteRecord struct {
	VoteID     int64
	ReviewID   int64
	AccountID  int64
	VoteType   string // helpful|funny|unhelpful
	OccurredAt time.Time
}

// EmitVotes casts votes on every review, streams them through emit, and
// mutates reviews[i].HelpfulCount / FunnyCount to the exact shown totals.
// Runs before the reviews are written (like comments) so the counts land.
// Returns the next free vote_id.
func (e *Emitter) EmitVotes(reviews []ReviewRecord, voteIDBase int64, emit func(VoteRecord)) int64 {
	if voteIDBase == 0 {
		voteIDBase = 1
	}
	voteID := voteIDBase
	if len(e.accountList) == 0 {
		return voteID
	}

	for i := range reviews {
		rev := &reviews[i]
		rr := rng.Derive(e.seed, "web/votes/"+itoa(rev.ReviewID))
		n := votesFor(rr, rev)
		if n == 0 {
			continue
		}
		floor := rev.PostedAt
		if floor.Before(CommentsLaunch) {
			floor = CommentsLaunch // votes launched with comments, 2008-03
		}
		span := SiteDark.Sub(floor)
		if span <= 0 {
			continue
		}
		helpful, funny := 0, 0
		seen := make(map[int64]bool, n)
		for k := 0; k < n; k++ {
			voter, ok := e.drawVoter(rr, floor, rev.AccountID, seen)
			if !ok {
				continue
			}
			seen[voter.id] = true
			vt := voteTypeFor(rr, rev.Archetype, rev.Rating, rev.Verified)
			at := floor.Add(time.Duration(rr.Float64() * float64(span))).Truncate(time.Second)
			emit(VoteRecord{VoteID: voteID, ReviewID: rev.ReviewID, AccountID: voter.id, VoteType: vt, OccurredAt: at})
			voteID++
			switch vt {
			case "helpful":
				helpful++
			case "funny":
				funny++
			}
		}
		rev.HelpfulCount = helpful
		rev.FunnyCount = funny
	}
	return voteID
}

// votesFor is the vote count for a review: a small heavy-tailed base, lifted
// by how much conversation the review drew (hot threads pull votes too) and
// by whether its archetype is a vote magnet.
func votesFor(rr *rand.Rand, rev *ReviewRecord) int {
	n := 0
	// base heavy tail
	switch x := rr.Float64(); {
	case x < 0.30:
		n = 0
	case x < 0.70:
		n = 1 + rr.IntN(4)
	case x < 0.92:
		n = 5 + rr.IntN(15)
	default:
		n = 20 + rr.IntN(60)
	}
	// hot threads and vote-magnet voices pull more
	n += rev.CommentCount / 3
	switch rev.Archetype {
	case "grouch", "technerd", "essayist", "troll":
		n += 2 + rr.IntN(8)
	}
	return n
}

// voteTypeFor picks a vote's kind from the review's voice, rating and
// authenticity. Fake-feeling reviews (unverified 5-stars — the shill shape)
// attract unhelpful; useful voices attract helpful; loud voices, funny.
func voteTypeFor(rr *rand.Rand, reviewArch string, rating int, verified bool) string {
	pHelpful, pFunny, pUnhelpful := 0.55, 0.25, 0.20
	switch reviewArch {
	case "grouch", "technerd", "essayist", "collector", "dealhunter", "parent":
		pHelpful, pFunny, pUnhelpful = 0.72, 0.12, 0.16
	case "capslock", "driveby", "superfan":
		pHelpful, pFunny, pUnhelpful = 0.38, 0.50, 0.12
	case "troll":
		pHelpful, pFunny, pUnhelpful = 0.10, 0.52, 0.38
	case "bucks_victim":
		pHelpful, pFunny, pUnhelpful = 0.68, 0.10, 0.22
	}
	// An unverified 5-star review (the shill shape) draws suspicion.
	if !verified && rating == 5 {
		pUnhelpful += 0.30
	}
	total := pHelpful + pFunny + pUnhelpful
	switch x := rr.Float64() * total; {
	case x < pHelpful:
		return "helpful"
	case x < pHelpful+pFunny:
		return "funny"
	default:
		return "unhelpful"
	}
}

// drawVoter samples an account holder who existed at `before`, isn't the
// review author, and hasn't already voted on this review.
func (e *Emitter) drawVoter(rr *rand.Rand, before time.Time, exclude int64, seen map[int64]bool) (acctRef, bool) {
	n := len(e.accountList)
	for try := 0; try < 8; try++ {
		a := e.accountList[rr.IntN(n)]
		if a.id == exclude || seen[a.id] || a.created.After(before) {
			continue
		}
		return a, true
	}
	return acctRef{}, false
}
