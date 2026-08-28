// Emission: turning the replayed customer/purchase population into web.*
// rows. Deterministic throughout — every decision derives its own RNG from
// (seed, "web/...") so a given seed produces byte-identical accounts,
// reviews, comments, votes and page-views. This file covers ACCOUNTS; the
// review/comment/vote/clickstream emitters build on the account map it
// produces.
package web

import (
	"math/rand/v2"
	"strconv"
	"time"

	"emporium/internal/customers"
	"emporium/internal/rng"
)

// Web-layer timeline (design §1; all consistent with dbo.source_systems).
var (
	WebLaunch      = time.Date(2001, 3, 1, 0, 0, 0, 0, time.UTC)  // nge.com + accounts
	CounterLaunch  = time.Date(2005, 11, 1, 0, 0, 0, 0, time.UTC) // reviews
	CommentsLaunch = time.Date(2008, 3, 1, 0, 0, 0, 0, time.UTC)  // comments + votes
	SiteDark       = time.Date(2016, 9, 30, 0, 0, 0, 0, time.UTC) // liquidation
)

// accountRateBySignupDecade gives P(customer ever created a web account),
// keyed by the customer's signup year. Pre-web signups came online only if
// still engaged when the site launched; native-web-era signups mostly made
// an account. Tuned to land ~35% of the whole book (design §6: ~14M of
// ~39M at 3T).
func accountRate(signupYear int) float64 {
	// Tuned against the 3g signup-year mix to land the whole book ~35%
	// (design §6). Rates are per-customer probabilities, so the fraction is
	// tier-stable as long as the SignupEras curve is.
	switch {
	case signupYear <= 1994:
		return 0.07 // paper-era loyalty; few ever came online
	case signupYear <= 2000:
		return 0.16
	case signupYear <= 2004:
		return 0.30 // the site exists at signup now
	case signupYear <= 2010:
		return 0.40
	default:
		return 0.34 // late signups into a shrinking business
	}
}

// Account is one web.accounts row (pre-loader).
type Account struct {
	AccountID    int64
	CustomerID   int64
	Username     string
	CreatedAt    time.Time
	Status       string // active|closed|banned
	SourceSystem string
}

// acctRef is the downstream-relevant slice of an account: its id, username
// and the context reviews/comments need (country, city, creation date). Held
// once in accountList (indexed by account_id − base); the customer→account_id
// map points into it.
type acctRef struct {
	id       int64
	username string
	country  string
	city     string
	created  time.Time
}

// Emitter carries the cross-entity state the web generators share: the
// loaded banks, the reception index, and (built during account emission)
// the customer→account map that reviews/comments/votes join through.
type Emitter struct {
	Banks     *Banks
	Reception *ReceptionIndex
	seed      uint64
	asOf      time.Time

	// accountByCustomer maps customer_id → account_id. accountList holds the
	// acctRef data once, indexed by (account_id − accountIDBase), so comments
	// can sample commenters by index and reviews can join by customer.
	accountByCustomer map[int64]int64
	accountList       []acctRef
	// taken enforces global username uniqueness across the account pass.
	// Deterministic because the customer replay is order-stable. baseCount
	// tracks per-base occupancy so MakeUnique widens its suffix span with the
	// crowd (amortised O(1), never saturates — see MakeUnique).
	taken     map[string]bool
	baseCount map[string]int

	// reviewers (built by MarkReviewers) and the pre-resolved catalog
	// lookups (SetCatalogs) power the review emitter.
	reviewers    map[int64]*reviewerState
	releaseMeta  map[int64]ReleaseMeta
	hardwareMeta map[int64]HardwareMeta
	// curatedByDate (famous, for unverified reviews + traffic weighting) and
	// allByDate (every catalog id, for clickstream) — both lazy, and sorted by
	// release date so a sale/view/review can be restricted to titles already
	// shipped at that moment (see sampleReleaseBefore).
	curatedByDate []datedRelease
	allByDate     []datedRelease

	accountIDBase int64
	nextAccountID int64
}

// NewEmitter builds an emitter for one web load. idBase is the starting
// account_id (1 for single-shard; a worker's private base otherwise).
func NewEmitter(banks *Banks, seed uint64, asOf time.Time, accountIDBase int64) *Emitter {
	if accountIDBase == 0 {
		accountIDBase = 1
	}
	return &Emitter{
		Banks:             banks,
		Reception:         BuildReceptionIndex(banks.Reception),
		seed:              seed,
		asOf:              asOf,
		accountByCustomer: make(map[int64]int64, 1<<20),
		accountList:       make([]acctRef, 0, 1<<20),
		taken:             make(map[string]bool, 1<<20),
		baseCount:         make(map[string]int, 1<<20),
		accountIDBase:     accountIDBase,
		nextAccountID:     accountIDBase,
	}
}

// SeedTakenUsernames pre-loads already-committed usernames on a resumed
// load so uniqueness holds across the resume boundary.
func (e *Emitter) SeedTakenUsernames(names []string) {
	for _, n := range names {
		e.taken[n] = true
	}
}

// ReleaseAccountScratch drops the username-uniqueness working set (`taken`,
// `baseCount`) once every account has been emitted. Those maps are used only
// by AccountFor→MakeUnique; the capture/review/comment/vote phases never touch
// them. Freeing them (with the caller's `accounts` slice) before the parallel
// capture reclaims ~1 GB at 3T. accountByCustomer and accountList are kept —
// reviews join through them.
func (e *Emitter) ReleaseAccountScratch() {
	e.taken = nil
	e.baseCount = nil
}

// AccountFor deterministically decides whether a customer has a web account
// and, if so, builds it. Returns (Account, true) when the customer is an
// account holder. Records the customer→account mapping for later phases.
//
// The username is built from the customer's first name + birth year for the
// ~28% FirstnameYYYY share (anomaly A5, the PII leak): even if the customer
// is later anonymised in dbo.customers, the handle here keeps the name.
func (e *Emitter) AccountFor(c customers.Customer) (Account, bool) {
	signup := parseCustDate(c.SignedUpAt)
	if signup.After(SiteDark) {
		return Account{}, false // signed up after the site was gone
	}
	r := rng.Derive(e.seed, "web/account/"+itoa(c.CustomerID))
	if r.Float64() >= accountRate(signup.Year()) {
		return Account{}, false
	}

	created := accountCreatedAt(r, signup)
	in := UsernameInput{
		SignupYear: created.Year(),
		IsTroll:    false, // archetype isn't known until review time; trolls self-select there
	}
	if c.FirstName != nil {
		in.FirstName = *c.FirstName
	}
	in.BirthYear = birthYear(c.DateOfBirth)

	uname := MakeUnique(r, e.taken, e.baseCount, GenerateUsername(r, e.Banks, in))

	acct := Account{
		AccountID:    e.nextAccountID,
		CustomerID:   c.CustomerID,
		Username:     uname,
		CreatedAt:    created,
		Status:       accountStatus(r),
		SourceSystem: webSourceSystem(created),
	}
	e.accountByCustomer[c.CustomerID] = e.nextAccountID
	e.accountList = append(e.accountList, acctRef{
		id: e.nextAccountID, username: uname, country: c.CountryCode,
		city: c.Address.City, created: created,
	})
	e.nextAccountID++
	return acct, true
}

// acct returns the acctRef for a customer that has an account (callers hold
// the reviewer/account invariant, so no presence check).
func (e *Emitter) acct(customerID int64) acctRef {
	return e.accountList[e.accountByCustomer[customerID]-e.accountIDBase]
}

// acctByID returns the acctRef for an account_id.
func (e *Emitter) acctByID(accountID int64) acctRef {
	return e.accountList[accountID-e.accountIDBase]
}

// AccountID returns the account_id for a customer, or (0,false) if the
// customer has no account. Used by the review/comment/vote emitters.
func (e *Emitter) AccountID(customerID int64) (int64, bool) {
	id, ok := e.accountByCustomer[customerID]
	return id, ok
}

// AccountCount is the number of accounts emitted so far.
func (e *Emitter) AccountCount() int { return len(e.accountByCustomer) }

// accountCreatedAt picks a creation date in [max(signup, WebLaunch),
// SiteDark], skewed early (people who join a site tend to join near when
// they first could).
func accountCreatedAt(r *rand.Rand, signup time.Time) time.Time {
	start := signup
	if start.Before(WebLaunch) {
		start = WebLaunch
	}
	span := SiteDark.Sub(start)
	if span <= 0 {
		return start
	}
	// Beta-ish skew toward the start: min of two uniforms.
	f := r.Float64()
	if g := r.Float64(); g < f {
		f = g
	}
	offset := time.Duration(f * float64(span))
	// Land on a whole minute of a plausible hour (web-log precision).
	created := start.Add(offset)
	hour := 8 + r.IntN(15) // 08:00–22:59, daytime-ish web traffic
	return time.Date(created.Year(), created.Month(), created.Day(), hour, r.IntN(60), r.IntN(60), 0, time.UTC)
}

// accountStatus: most active; a small banned/closed tail. Trolls skew the
// ban rate up but archetype isn't known here — the review emitter can flip
// an account to 'banned' when it attaches a banned troll (design §4). This
// baseline gives the non-troll background rate.
func accountStatus(r *rand.Rand) string {
	switch x := r.Float64(); {
	case x < 0.006:
		return "banned"
	case x < 0.030:
		return "closed"
	default:
		return "active"
	}
}

// webSourceSystem maps an account-creation date to the storefront that
// captured it (matches dbo.source_systems rows exactly).
func webSourceSystem(t time.Time) string {
	switch {
	case t.Before(time.Date(2007, 10, 1, 0, 0, 0, 0, time.UTC)):
		return "web_legacy_2001_2007"
	case t.Before(time.Date(2010, 4, 1, 0, 0, 0, 0, time.UTC)):
		return "web_2008_plus"
	default:
		// Post-2010 accounts split between web and app; keep it web-weighted.
		return "web_2008_plus"
	}
}

// --- small helpers ---------------------------------------------------------

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// parseCustDate parses customers.SignedUpAt / anonymised_at style strings.
// The generator emits either "YYYY-MM-DD HH:MM:SS[.fff]" or "YYYY-MM-DD";
// older (coarsened) records may be date-only. Returns zero time on failure.
func parseCustDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	// customers.formatTimestamp emits these two shapes (always T...Z, with
	// the time zero-filled to whatever precision the record was coarsened
	// to); the rest are defensive fallbacks.
	for _, layout := range []string{
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05.999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// birthYear extracts the year from a "YYYY-MM-DD" date-of-birth pointer.
// Returns 0 when unknown (never captured / anonymised).
func birthYear(dob *string) int {
	if dob == nil || len(*dob) < 4 {
		return 0
	}
	y, err := strconv.Atoi((*dob)[:4])
	if err != nil {
		return 0
	}
	return y
}
