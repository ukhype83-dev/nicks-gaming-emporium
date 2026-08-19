// Comment threads (design §4). Comments are the Steam/SO Comments analog:
// flat, conversational, power-law in volume (most reviews get none, a rare
// few become 50-400-comment flame wars — the pagination lab). Each comment is
// a move (agree/disagree/dunk/defend/…) chosen from the parent review's
// sentiment and the previous commenter, so a thread reads as an argument.
// Commenters are other account holders; @mentions splice their real handles.
package web

import (
	"math/rand/v2"
	"time"

	"emporium/internal/rng"
)

// CommentRecord is one web.review_comments row.
type CommentRecord struct {
	CommentID    int64
	ReviewID     int64
	AccountID    int64
	Body         string
	PostedAt     time.Time
	SourceSystem string
}

// EmitComments walks the reviews in order, generates each one's thread, and
// streams the comments through emit. Mutates reviews[i].CommentCount to the
// exact number emitted (the denormalised total the loader persists). Returns
// the next free comment_id. Must run before the reviews are written so the
// counts land on the review rows.
func (e *Emitter) EmitComments(reviews []ReviewRecord, commentIDBase int64, emit func(CommentRecord)) int64 {
	if commentIDBase == 0 {
		commentIDBase = 1
	}
	commentID := commentIDBase
	if len(e.accountList) == 0 {
		return commentID
	}

	for i := range reviews {
		rev := &reviews[i]
		rr := rng.Derive(e.seed, "web/thread/"+itoa(rev.ReviewID))
		count := commentCountFor(rr)
		if count == 0 {
			continue
		}
		isHot := count >= hotThreadMin
		authorUser := e.acctByID(rev.AccountID).username
		tctx := e.reviewTargetCtx(*rev)
		// Comment thread language = the review's, if that language has a
		// comment bank; else English (international commenters).
		cbank := e.Banks.forLang(rev.Language)
		if len(cbank.comments) == 0 {
			cbank = e.Banks.forLang("en")
		}

		cur := rev.PostedAt
		if cur.Before(CommentsLaunch) {
			cur = CommentsLaunch
		}
		prevUser := authorUser
		prevArch := rev.Archetype
		prevKind := ""
		emitted := 0
		for pos := 0; pos < count; pos++ {
			commenter, ok := e.drawCommenter(rr, cur, rev.AccountID)
			if !ok {
				continue
			}
			arch := e.pickArchetypeKeyed("web/commentarch/"+itoa(commenter.id), 0)
			kind := pickCommentKind(rr, pos, rev.Sentiment, arch.ID, prevArch, prevKind)

			cur = advanceCommentTime(rr, cur, isHot)
			if cur.After(SiteDark) {
				cur = SiteDark
			}
			// A review posted late on the final day (its time-of-day stamp can
			// sit past the SiteDark midnight anchor) must not end up with
			// comments clamped to before it.
			if cur.Before(rev.PostedAt) {
				cur = rev.PostedAt
			}
			ctx := Context{"prev_user": prevUser, "parent_user": authorUser, "city": commenter.city}
			for k, v := range tctx {
				ctx[k] = v
			}
			body := cbank.GenerateComment(rr, kind, arch, rev.Sentiment, cur.Year(), ctx)
			if body == "" {
				continue
			}
			emit(CommentRecord{
				CommentID: commentID, ReviewID: rev.ReviewID, AccountID: commenter.id,
				Body: body, PostedAt: cur.Truncate(time.Second), SourceSystem: webSourceSystem(cur),
			})
			commentID++
			emitted++
			prevUser = commenter.username
			prevArch = arch.ID
			prevKind = kind
		}
		rev.CommentCount = emitted
	}
	return commentID
}

const hotThreadMin = 40

// commentCountFor is the per-review comment total: a heavy-tailed
// distribution where most reviews draw nothing and a rare ~0.3% ignite into
// hot threads (design §4).
func commentCountFor(rr *rand.Rand) int {
	if rr.Float64() < 0.003 {
		return hotThreadMin + rr.IntN(360) // 40-399: the flame wars
	}
	switch x := rr.Float64(); {
	case x < 0.68:
		return 0
	case x < 0.85:
		return 1
	case x < 0.94:
		return 2
	case x < 0.98:
		return 3
	default:
		return 4 + rr.IntN(6)
	}
}

// drawCommenter samples an account holder who already existed at `before` and
// isn't the review's author. A few tries, then gives up (the thread simply
// gets one fewer comment).
func (e *Emitter) drawCommenter(rr *rand.Rand, before time.Time, exclude int64) (acctRef, bool) {
	n := len(e.accountList)
	for try := 0; try < 8; try++ {
		a := e.accountList[rr.IntN(n)]
		if a.id == exclude || a.created.After(before) {
			continue
		}
		return a, true
	}
	return acctRef{}, false
}

// pickCommentKind chooses the conversational move. An answer only ever
// follows a question (so "yes, after the patch" can't reply to a rhetorical
// dunk); trolls dunk; a regular replying to a troll defends; otherwise it
// depends on the parent review's sentiment and the thread position.
func pickCommentKind(rr *rand.Rand, pos int, parentSent, commenterArch, prevArch, prevKind string) string {
	if prevKind == "question" && rr.Float64() < 0.7 {
		return "answer"
	}
	if commenterArch == "troll" && rr.Float64() < 0.6 {
		return pick2(rr, "troll_reply", "dunk")
	}
	if prevArch == "troll" && rr.Float64() < 0.5 {
		return pick2(rr, "defend", "disagree")
	}
	if pos == 0 {
		switch parentSent {
		case "pos":
			return pickN(rr, "agree", "question", "anecdote", "disagree")
		case "neg":
			return pickN(rr, "agree", "disagree", "question", "anecdote")
		default:
			return pickN(rr, "question", "disagree", "agree", "anecdote")
		}
	}
	// Deeper in the thread: replies and arguments (answer excluded here — it
	// is reachable only as a reply to a question, above).
	switch parentSent {
	case "pos":
		return pickN(rr, "agree", "disagree", "dunk", "anecdote", "question")
	case "neg":
		return pickN(rr, "agree", "defend", "dunk", "disagree", "question")
	default:
		return pickN(rr, "disagree", "dunk", "agree", "defend", "question")
	}
}

// advanceCommentTime steps the clock forward for the next comment. Hot
// threads move in minutes (a live argument); quiet ones trickle over hours to
// days.
func advanceCommentTime(rr *rand.Rand, cur time.Time, isHot bool) time.Time {
	if isHot {
		return cur.Add(time.Duration(2+rr.IntN(240)) * time.Minute)
	}
	return cur.Add(time.Duration(1+rr.IntN(72)) * time.Hour)
}

// reviewTargetCtx pulls the platform/publisher/title of whatever a review is
// about, so comment fragments can reference it.
func (e *Emitter) reviewTargetCtx(rev ReviewRecord) Context {
	ctx := Context{}
	if rev.ReleaseID != 0 {
		if m, ok := e.releaseMeta[rev.ReleaseID]; ok {
			ctx["title"] = cleanDisplayTitle(m.Title)
			ctx["platform"] = orDefault(m.Platform, "console")
			ctx["publisher"] = orDefault(m.Publisher, "the publisher")
			ctx["genre"] = orDefault(m.Genre, "game")
		}
	} else if rev.HardwareID != 0 {
		if m, ok := e.hardwareMeta[rev.HardwareID]; ok {
			ctx["title"] = m.ModelName
			ctx["platform"] = orDefault(m.Platform, "the system")
			ctx["publisher"] = orDefault(m.Manufacturer, "the manufacturer")
		}
	}
	return ctx
}

func pick2(rr *rand.Rand, a, b string) string {
	if rr.IntN(2) == 0 {
		return a
	}
	return b
}

// pickN returns one of the options, front-weighted (earlier options more
// likely) so the first-listed move dominates without excluding the rest.
func pickN(rr *rand.Rand, options ...string) string {
	weights := make([]float64, len(options))
	total := 0.0
	w := 1.0
	for i := range options {
		weights[i] = w
		total += w
		w *= 0.6
	}
	roll := rr.Float64() * total
	for i, opt := range options {
		roll -= weights[i]
		if roll <= 0 {
			return opt
		}
	}
	return options[len(options)-1]
}
