package dbwriter

import "emporium/internal/web"

// Row-builders for the V1.26 web (community) layer. Mirrors the
// hardware/transaction converters: a Columns slice paired with a ToRows
// function that yields [][]any for BulkInsert. web.* times are already
// time.Time (web-log precision), so they pass straight through — no
// string re-parse like the transaction path needs.

var AccountColumns = []string{
	"account_id", "customer_id", "username", "created_at", "status", "source_system",
}

// AccountRow builds one web.accounts row (batched/streaming load path).
func AccountRow(a web.Account) []any {
	return []any{a.AccountID, a.CustomerID, a.Username, a.CreatedAt, a.Status, a.SourceSystem}
}

// AccountsToRows converts emitted accounts to web.accounts rows.
func AccountsToRows(accounts []web.Account) [][]any {
	rows := make([][]any, 0, len(accounts))
	for _, a := range accounts {
		rows = append(rows, AccountRow(a))
	}
	return rows
}

var ReviewColumns = []string{
	"review_id", "account_id", "release_id", "hardware_id", "rating",
	"title", "body", "language_code", "is_verified_purchase", "posted_at",
	"edited_at", "comment_count", "helpful_count", "funny_count", "source_system",
}

// ReviewRow builds one web.reviews row (batched/streaming load path). The
// comment/helpful/funny counts are the denormalised totals the comment and vote
// passes set; edited_at is NULL (review edits aren't modelled in tranche 1).
func ReviewRow(r web.ReviewRecord) []any {
	var title any
	if r.Title != "" {
		title = r.Title
	}
	return []any{
		r.ReviewID, r.AccountID, nullIfZero(r.ReleaseID), nullIfZero(r.HardwareID), r.Rating,
		title, r.Body, r.Language, boolToBit(r.Verified), r.PostedAt,
		nil, r.CommentCount, r.HelpfulCount, r.FunnyCount, r.SourceSystem,
	}
}

// ReviewsToRows converts emitted reviews to web.reviews rows.
func ReviewsToRows(reviews []web.ReviewRecord) [][]any {
	rows := make([][]any, 0, len(reviews))
	for _, r := range reviews {
		rows = append(rows, ReviewRow(r))
	}
	return rows
}

func boolToBit(b bool) int {
	if b {
		return 1
	}
	return 0
}

var ReviewCommentColumns = []string{
	"comment_id", "review_id", "account_id", "body", "posted_at", "source_system",
}

func ReviewCommentsToRows(comments []web.CommentRecord) [][]any {
	rows := make([][]any, 0, len(comments))
	for _, c := range comments {
		rows = append(rows, ReviewCommentRow(c))
	}
	return rows
}

// ReviewCommentRow builds one web.review_comments row (streaming load path).
func ReviewCommentRow(c web.CommentRecord) []any {
	return []any{c.CommentID, c.ReviewID, c.AccountID, c.Body, c.PostedAt, c.SourceSystem}
}

var ReviewVoteColumns = []string{
	"vote_id", "review_id", "account_id", "vote_type", "occurred_at",
}

func ReviewVotesToRows(votes []web.VoteRecord) [][]any {
	rows := make([][]any, 0, len(votes))
	for _, v := range votes {
		rows = append(rows, ReviewVoteRow(v))
	}
	return rows
}

// ReviewVoteRow builds one web.review_votes row (streaming load path).
func ReviewVoteRow(v web.VoteRecord) []any {
	return []any{v.VoteID, v.ReviewID, v.AccountID, v.VoteType, v.OccurredAt}
}

var PageViewColumns = []string{
	"page_view_id", "session_id", "account_id", "occurred_at", "url_path",
	"http_status", "referrer_domain", "user_agent_family", "client_country",
	"bytes_sent", "source_system",
}

func PageViewsToRows(views []web.PageViewRecord) [][]any {
	rows := make([][]any, 0, len(views))
	for _, v := range views {
		rows = append(rows, PageViewRow(v))
	}
	return rows
}

// PageViewRow builds one web.page_views row. Exposed for the streaming
// clickstream load path (the table is too large to hold in memory).
func PageViewRow(v web.PageViewRecord) []any {
	var ref any
	if v.ReferrerDomain != "" {
		ref = v.ReferrerDomain
	}
	return []any{
		v.PageViewID, v.SessionID, nullIfZero(v.AccountID), v.OccurredAt, v.URLPath,
		v.HTTPStatus, ref, v.UserAgentFamily, v.ClientCountry,
		v.BytesSent, v.SourceSystem,
	}
}
