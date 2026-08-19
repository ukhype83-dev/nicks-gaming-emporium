// Customer-engagement records emitted alongside each customer:
//
//   - customer_lifecycle_events  — append-only audit (signup +
//     anonymisation events when AnonymisedAt is set)
//   - customer_emails            — primary email + occasional secondary
//   - communication_preferences  — channel × purpose preferences,
//     regime-aware defaults (GDPR opt-out by default for EU after 2018)
//   - consent_events             — append-only consent audit; pulse
//     bursts at 2018-05-25 (GDPR) and 2020-01-01 (CCPA)
//   - loyalty_memberships        — era-aware schemes (stamp_1986 →
//     card_1998 → unified_2011)
//   - saved_payment_methods      — online-era only (post-2007)
//
// All six are generated in customers.Generate alongside the existing
// Address. The DB writer splits them into their respective tables on
// load. See §9.12.5 (anonymisation), §9.12.6 (consent), §9.10.4
// (payment methods) for the design intent.
package customers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"
)

// --------------------------------------------------------------------
// Type definitions
// --------------------------------------------------------------------

// CustomerLifecycleEvent maps to dbo.customer_lifecycle_events.
type CustomerLifecycleEvent struct {
	LifecycleEventID int64   `json:"lifecycle_event_id"`
	EventType        string  `json:"event_type"`  // signup|reactivated|marked_dormant|anonymisation_requested|anonymised|deleted
	OccurredAt       string  `json:"occurred_at"` // ISO ms
	Reason           *string `json:"reason"`
	SourceSystem     string  `json:"source_system"`
}

// CustomerEmail maps to dbo.customer_emails.
type CustomerEmail struct {
	CustomerEmailID int64   `json:"customer_email_id"`
	Email           *string `json:"email"`
	IsPrimary       bool    `json:"is_primary"`
	VerifiedAt      *string `json:"verified_at"`
	AnonymisedAt    *string `json:"anonymised_at"`
}

// CommunicationPreference maps to dbo.communication_preferences.
type CommunicationPreference struct {
	CommPrefID int64  `json:"comm_pref_id"`
	Channel    string `json:"channel"` // email|sms|push|post
	Purpose    string `json:"purpose"` // marketing|service|trade_in_alerts
	OptIn      bool   `json:"opt_in"`
	UpdatedAt  string `json:"updated_at"` // ISO ms
}

// ConsentEvent maps to dbo.consent_events. Append-only audit.
type ConsentEvent struct {
	ConsentEventID int64  `json:"consent_event_id"`
	Channel        string `json:"channel"`
	Purpose        string `json:"purpose"`
	EventType      string `json:"event_type"` // granted|revoked
	OccurredAt     string `json:"occurred_at"`
	SourceSystem   string `json:"source_system"`
}

// LoyaltyMembership maps to dbo.loyalty_memberships.
type LoyaltyMembership struct {
	MembershipID  int64   `json:"membership_id"`
	Scheme        string  `json:"scheme"` // stamp_1986|card_1998|unified_2011
	EnrolledAt    string  `json:"enrolled_at"`
	Tier          *string `json:"tier"` // bronze|silver|gold|platinum
	PointsBalance int     `json:"points_balance"`
	ClosedAt      *string `json:"closed_at"`
}

// SavedPaymentMethod maps to dbo.saved_payment_methods.
type SavedPaymentMethod struct {
	SavedPaymentID int64   `json:"saved_payment_id"`
	Method         string  `json:"method"`
	Token          string  `json:"token"`
	CardLast4      *string `json:"card_last4"`
	ExpiryMonth    *int    `json:"expiry_month"`
	ExpiryYear     *int    `json:"expiry_year"`
	AddedAt        string  `json:"added_at"`
	RemovedAt      *string `json:"removed_at"`
}

// engagementCounters tracks the next ID to assign for each engagement
// table. Passed by pointer so all customers in a Generate run share
// the same monotonic ID space per table.
type engagementCounters struct {
	lifecycle  int64
	email      int64
	commPref   int64
	consent    int64
	loyalty    int64
	savedPay   int64
}

// --------------------------------------------------------------------
// Generators
// --------------------------------------------------------------------

// generateLifecycleEvents emits the canonical signup event plus any
// anonymisation events that should be present given the customer's
// state. Future V1.x: dormant detection + reactivation events when
// activity tracking is wired up.
func generateLifecycleEvents(c *Customer, ctr *engagementCounters, r *rand.Rand) []CustomerLifecycleEvent {
	events := make([]CustomerLifecycleEvent, 0, 2)
	// Signup — always present.
	ctr.lifecycle++
	events = append(events, CustomerLifecycleEvent{
		LifecycleEventID: ctr.lifecycle,
		EventType:        "signup",
		OccurredAt:       c.SignedUpAt,
		Reason:           nil,
		SourceSystem:     c.SourceSystem,
	})
	if c.AnonymisedAt != nil {
		// Request comes shortly before completion (1–14 days earlier).
		anon, err := time.Parse(time.RFC3339Nano, *c.AnonymisedAt)
		if err != nil {
			anon, _ = time.Parse("2006-01-02T15:04:05Z", *c.AnonymisedAt)
		}
		requestOffset := -1 - r.IntN(14)
		requestAt := anon.AddDate(0, 0, requestOffset).UTC().Format("2006-01-02T15:04:05.000Z")
		// Reason chosen by regime:
		//   gdpr/uk_gdpr  → gdpr_erasure
		//   ccpa          → ccpa_request
		//   anything else → retention_expiry
		reason := reasonForRegime(c.GoverningRegime)
		ctr.lifecycle++
		events = append(events, CustomerLifecycleEvent{
			LifecycleEventID: ctr.lifecycle,
			EventType:        "anonymisation_requested",
			OccurredAt:       requestAt,
			Reason:           &reason,
			SourceSystem:     c.SourceSystem,
		})
		ctr.lifecycle++
		events = append(events, CustomerLifecycleEvent{
			LifecycleEventID: ctr.lifecycle,
			EventType:        "anonymised",
			OccurredAt:       *c.AnonymisedAt,
			Reason:           &reason,
			SourceSystem:     c.SourceSystem,
		})
	}
	return events
}

func reasonForRegime(regime string) string {
	switch regime {
	case "gdpr", "uk_gdpr":
		return "gdpr_erasure"
	case "ccpa":
		return "ccpa_request"
	default:
		return "retention_expiry"
	}
}

// generateEmails emits 0-2 emails per customer based on signup era.
// Pre-1995 paper-era signups have no email; 2011+ signups almost
// universally do. Anonymised customers get email = NULL with
// anonymised_at set.
func generateEmails(c *Customer, first, last string, ctr *engagementCounters, r *rand.Rand) []CustomerEmail {
	signupYear := signupYearFor(c.SignedUpAt)
	hasEmailProb := emailProbabilityForYear(signupYear)
	if r.Float64() >= hasEmailProb {
		return nil
	}
	emails := make([]CustomerEmail, 0, 2)
	primary := buildEmail(first, last, c.CountryCode, c.CustomerID, 0, signupYear, r)
	primaryStr := primary
	var anon *string
	if c.AnonymisedAt != nil {
		// Email gets NULLed at anonymisation; the row stays as the
		// regulatory "this person did exist" pointer.
		primaryStr = ""
		anon = c.AnonymisedAt
	}
	ctr.email++
	pe := CustomerEmail{
		CustomerEmailID: ctr.email,
		IsPrimary:       true,
		VerifiedAt:      verifiedAtFor(c.SignedUpAt, r),
		AnonymisedAt:    anon,
	}
	if primaryStr != "" {
		pe.Email = &primaryStr
	}
	emails = append(emails, pe)
	// Secondary email for ~10% of post-2003 customers (work address).
	if signupYear >= 2003 && r.Float64() < 0.10 {
		secondary := buildEmail(first, last, c.CountryCode, c.CustomerID, 1, signupYear, r)
		secondaryStr := secondary
		var anon2 *string
		if c.AnonymisedAt != nil {
			secondaryStr = ""
			anon2 = c.AnonymisedAt
		}
		ctr.email++
		se := CustomerEmail{
			CustomerEmailID: ctr.email,
			IsPrimary:       false,
			VerifiedAt:      verifiedAtFor(c.SignedUpAt, r),
			AnonymisedAt:    anon2,
		}
		if secondaryStr != "" {
			se.Email = &secondaryStr
		}
		emails = append(emails, se)
	}
	return emails
}

// emailProbabilityForYear returns the per-signup email adoption rate
// for a given year. V1.9.0 replaces V1.7/V1.8's three-step constant
// (2% / 45% / 85% / 96%) with a proper sigmoidal adoption curve:
//
//   - Pre-1995: 0% (no consumer email yet — CompuServe etc. were tiny
//     niche, and NGE wouldn't capture those addresses).
//   - 1995-2020: logistic curve centred on 2003 (the "Hotmail/Yahoo
//     mainstream" inflection year), steepness 0.5/year.
//   - 2020+: 95% (saturation — privacy-conscious + no-tech minority).
//
// Sample values: 1996 ≈ 3%, 2000 ≈ 18%, 2003 ≈ 50%, 2007 ≈ 88%,
// 2012 ≈ 99% → capped at 95%. Matches real adoption pace reasonably.
func emailProbabilityForYear(year int) float64 {
	if year < 1995 {
		return 0.0
	}
	if year >= 2020 {
		return 0.95
	}
	s := 1.0 / (1.0 + math.Exp(-0.5*float64(year-2003)))
	if s < 0.02 {
		s = 0.02
	}
	if s > 0.95 {
		s = 0.95
	}
	return s
}

// buildEmail constructs a deterministic-ish email address. variant=0
// is the primary "personal" email; variant=1 is a "work" secondary.
//
// V1.9.0 adds era-aware domain selection. Pre-V1.9 all addresses used
// `example.*` RFC-2606 reserved domains, which made domain-mix audits
// impossible. V1.9 samples from a period-correct provider pool:
//
//   - 1995-1999: AOL / Hotmail (1996) / Yahoo (1997) / Earthlink /
//     CompuServe / .edu-ish placeholders + ISP-based.
//   - 2000-2006: AOL declining; Hotmail/Yahoo dominant; some MSN.
//   - 2007-2010: Gmail rises (public from 2007); AOL legacy tail.
//   - 2011-2017: Gmail dominant; iCloud arrives 2011; Outlook
//     supersedes Hotmail 2013.
//   - 2018+: Gmail + Outlook + iCloud + ProtonMail (privacy-conscious).
//
// Domains are .example-suffixed so they don't accidentally resolve.
// Cardinality: 5-8 per era × era ~= ~30 distinct domains lifetime.
// `signupYear` is passed in so the domain matches when the customer
// signed up, not when the simulator runs.
func buildEmail(first, last, country string, customerID int64, variant int, signupYear int, r *rand.Rand) string {
	// Deterministic suffix from customer_id + variant — keeps emails
	// unique without depending on the RNG sequence.
	suffix := customerID*10 + int64(variant)
	var domains []string
	if variant == 1 {
		// Work emails — corporate-ish, no era-tracking.
		domains = []string{"work.example", "corp.example", "company.example"}
	} else {
		domains = emailDomainsForYear(signupYear)
	}
	domain := domains[r.IntN(len(domains))]
	local := strings.ToLower(strings.TrimSpace(first))
	if last != "" {
		local += "." + strings.ToLower(strings.TrimSpace(last))
	}
	local = sanitiseEmailLocal(local)
	if local == "" {
		local = "user"
	}
	return fmt.Sprintf("%s%d@%s", local, suffix, domain)
}

// emailDomainsForYear returns the era-appropriate consumer-email
// provider pool. Domains are suffixed `.example` so they don't resolve
// to anyone's real email. The pool reflects roughly the dominant
// services of each era; not an exhaustive market-share weighting.
func emailDomainsForYear(year int) []string {
	switch {
	case year <= 1999:
		// Early dial-up / web-mail era.
		return []string{
			"aol.example", "hotmail.example", "compuserve.example",
			"earthlink.example", "yahoo.example", "msn.example",
		}
	case year <= 2006:
		// Web-mail mainstream. AOL fading, Yahoo/Hotmail dominant.
		return []string{
			"hotmail.example", "yahoo.example", "msn.example",
			"aol.example", "earthlink.example",
		}
	case year <= 2010:
		// Gmail emerges (public 2007). Hotmail/Yahoo still big.
		return []string{
			"gmail.example", "hotmail.example", "yahoo.example",
			"msn.example", "aol.example",
		}
	case year <= 2017:
		// Gmail dominant. iCloud (2011), Outlook supersedes Hotmail (2013).
		return []string{
			"gmail.example", "gmail.example", "gmail.example", // weight Gmail
			"outlook.example", "yahoo.example", "icloud.example",
			"hotmail.example",
		}
	default:
		// 2018+: Gmail dominant; iCloud growing; privacy-focused emerge.
		return []string{
			"gmail.example", "gmail.example", "gmail.example", // weight Gmail
			"outlook.example", "icloud.example", "icloud.example",
			"yahoo.example", "protonmail.example",
		}
	}
}

func sanitiseEmailLocal(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch {
		case b >= 'a' && b <= 'z':
			out = append(out, b)
		case b >= '0' && b <= '9':
			out = append(out, b)
		case b == '.' || b == '-' || b == '_':
			out = append(out, b)
			// Skip non-ASCII / accented characters silently.
		}
	}
	return string(out)
}

// verifiedAtFor: 80% of emails are verified within a few days of signup.
func verifiedAtFor(signupAt string, r *rand.Rand) *string {
	if r.Float64() >= 0.80 {
		return nil
	}
	signup, err := time.Parse(time.RFC3339Nano, signupAt)
	if err != nil {
		signup, err = time.Parse("2006-01-02T15:04:05Z", signupAt)
		if err != nil {
			return nil
		}
	}
	delay := time.Duration(r.IntN(5*24*60)) * time.Minute
	v := signup.Add(delay).UTC().Format("2006-01-02T15:04:05.000Z")
	return &v
}

// generateCommPrefs emits one row per (channel, purpose) the customer
// has an explicit preference for. Defaults are regime-aware: post-GDPR
// EU customers default to opt-out for marketing channels.
func generateCommPrefs(c *Customer, hasEmail bool, ctr *engagementCounters, r *rand.Rand) []CommunicationPreference {
	if !hasEmail {
		// No email captured → no comm preferences either (paper era).
		return nil
	}
	signupYear := signupYearFor(c.SignedUpAt)
	updatedAt := c.SignedUpAt
	gdprStrict := isStrictRegime(c.GoverningRegime) && signupYear >= 2018
	prefs := make([]CommunicationPreference, 0, 5)
	add := func(channel, purpose string, optIn bool) {
		ctr.commPref++
		prefs = append(prefs, CommunicationPreference{
			CommPrefID: ctr.commPref,
			Channel:    channel,
			Purpose:    purpose,
			OptIn:      optIn,
			UpdatedAt:  updatedAt,
		})
	}
	// email/marketing — default opt-in 60%, post-GDPR EU default opt-out.
	marketingProb := 0.60
	if gdprStrict {
		marketingProb = 0.25
	}
	add("email", "marketing", r.Float64() < marketingProb)
	// email/service — almost universally opt-in (transactional).
	add("email", "service", r.Float64() < 0.97)
	// email/trade_in_alerts — niche.
	add("email", "trade_in_alerts", r.Float64() < 0.30)
	// sms/marketing — only post-2010 customers, ~15%.
	if signupYear >= 2010 {
		add("sms", "marketing", r.Float64() < 0.15)
	}
	// post/marketing — only sensible for old-era signups.
	if signupYear < 2005 {
		add("post", "marketing", r.Float64() < 0.40)
	} else {
		add("post", "marketing", r.Float64() < 0.05)
	}
	return prefs
}

func isStrictRegime(regime string) bool {
	return regime == "gdpr" || regime == "uk_gdpr" || regime == "lgpd"
}

// generateConsentEvents emits the audit trail. One "granted" per active
// preference at signup, plus regime-driven re-consent pulses.
func generateConsentEvents(c *Customer, prefs []CommunicationPreference, ctr *engagementCounters, r *rand.Rand) []ConsentEvent {
	if len(prefs) == 0 {
		return nil
	}
	events := make([]ConsentEvent, 0, len(prefs)+2)
	// Initial grants — one per opt-in preference at signup.
	for _, p := range prefs {
		if !p.OptIn {
			continue
		}
		ctr.consent++
		events = append(events, ConsentEvent{
			ConsentEventID: ctr.consent,
			Channel:        p.Channel,
			Purpose:        p.Purpose,
			EventType:      "granted",
			OccurredAt:     c.SignedUpAt,
			SourceSystem:   c.SourceSystem,
		})
	}
	// GDPR / UK_GDPR pulse: 2018-05-25 re-consent for EU customers
	// active by then. Mostly granted (re-affirmed); some revocations.
	signupYear := signupYearFor(c.SignedUpAt)
	if signupYear < 2018 && (c.GoverningRegime == "gdpr" || c.GoverningRegime == "uk_gdpr") {
		// 50/50 odds the customer engaged with the re-consent flow.
		if r.Float64() < 0.55 {
			pulseAt := "2018-05-25T09:00:00.000Z"
			eventType := "granted"
			if r.Float64() < 0.20 {
				eventType = "revoked"
			}
			ctr.consent++
			events = append(events, ConsentEvent{
				ConsentEventID: ctr.consent,
				Channel:        "email",
				Purpose:        "marketing",
				EventType:      eventType,
				OccurredAt:     pulseAt,
				SourceSystem:   c.SourceSystem,
			})
		}
	}
	// CCPA pulse: 2020-01-01 for CA customers active by then.
	if signupYear < 2020 && c.GoverningRegime == "ccpa" {
		if r.Float64() < 0.30 {
			pulseAt := "2020-01-01T09:00:00.000Z"
			eventType := "granted"
			if r.Float64() < 0.15 {
				eventType = "revoked"
			}
			ctr.consent++
			events = append(events, ConsentEvent{
				ConsentEventID: ctr.consent,
				Channel:        "email",
				Purpose:        "marketing",
				EventType:      eventType,
				OccurredAt:     pulseAt,
				SourceSystem:   c.SourceSystem,
			})
		}
	}
	return events
}

// generateLoyaltyMemberships emits 0-3 enrollments based on the
// customer's lifespan crossing the three loyalty-scheme eras.
//
// V1.9.0 recalibration: V1.7/V1.8 enrolled 30-60K loyalty memberships
// per year from 1986-1994, but formal retail loyalty schemes barely
// existed pre-1995 (GameStop's PowerUp launched 2010, Best Buy Reward
// Zone 2003). The three schemes are now era-gated more strictly:
//
//   - stamp_1986: rebrand-in-name-only, the actual programme didn't
//     start until 1995 (paper-stamp era). Pre-1995 customers don't
//     enroll. Enrollment date floor lifted to 1995.
//   - card_1998: barcode-card era (1998-2010). Unchanged.
//   - unified_2011: digital scheme (2011+). Unchanged.
func generateLoyaltyMemberships(c *Customer, ctr *engagementCounters, asOf time.Time, r *rand.Rand) []LoyaltyMembership {
	signup, err := time.Parse(time.RFC3339Nano, c.SignedUpAt)
	if err != nil {
		signup, _ = time.Parse("2006-01-02T15:04:05Z", c.SignedUpAt)
	}
	signupYear := signup.Year()
	out := make([]LoyaltyMembership, 0, 3)
	add := func(scheme string, enrolledAt time.Time) {
		tier := sampleLoyaltyTier(r)
		points := r.IntN(50000)
		ctr.loyalty++
		out = append(out, LoyaltyMembership{
			MembershipID:  ctr.loyalty,
			Scheme:        scheme,
			EnrolledAt:    enrolledAt.UTC().Format("2006-01-02T15:04:05.000Z"),
			Tier:          &tier,
			PointsBalance: points,
		})
	}
	// stamp_1986 — paper-stamp scheme. Only existed 1995-1997 in reality
	// (the "1986" name is marketing/historic, not literal). Customers
	// who signed up before 1995 missed it; customers who signed up
	// 1995-1997 enroll at 30%.
	if signupYear >= 1995 && signupYear <= 1997 && r.Float64() < 0.30 {
		enrol := signup
		if enrol.Year() < 1995 {
			enrol = time.Date(1995, time.January, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(r.IntN(180*24)) * time.Hour)
		}
		add("stamp_1986", enrol)
	}
	// card_1998 — barcoded card scheme. Active 1998-2010.
	if signupYear >= 1995 && signupYear <= 2010 && r.Float64() < 0.50 {
		enrol := signup
		if enrol.Year() < 1998 {
			enrol = time.Date(1998, time.January, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(r.IntN(180*24)) * time.Hour)
		}
		add("card_1998", enrol)
	}
	// unified_2011 — current digital scheme.
	if r.Float64() < 0.70 {
		enrol := signup
		if enrol.Year() < 2011 {
			enrol = time.Date(2011, time.June, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(r.IntN(360*24)) * time.Hour)
		}
		if !enrol.After(asOf) {
			add("unified_2011", enrol)
		}
	}
	return out
}

// sampleLoyaltyTier — bronze / silver / gold / platinum at 50/30/15/5.
func sampleLoyaltyTier(r *rand.Rand) string {
	u := r.Float64()
	switch {
	case u < 0.50:
		return "bronze"
	case u < 0.80:
		return "silver"
	case u < 0.95:
		return "gold"
	default:
		return "platinum"
	}
}

// generateSavedPaymentMethods — online-era (post-2007) customers only.
// 30% of eligible customers save at least one method; of those, 60/30/10
// have 1/2/3 stored.
func generateSavedPaymentMethods(c *Customer, ctr *engagementCounters, asOf time.Time, r *rand.Rand) []SavedPaymentMethod {
	signupYear := signupYearFor(c.SignedUpAt)
	if signupYear < 2007 {
		return nil
	}
	if r.Float64() >= 0.30 {
		return nil
	}
	count := 1
	switch u := r.Float64(); {
	case u < 0.60:
		count = 1
	case u < 0.90:
		count = 2
	default:
		count = 3
	}
	signup, err := time.Parse(time.RFC3339Nano, c.SignedUpAt)
	if err != nil {
		signup, _ = time.Parse("2006-01-02T15:04:05Z", c.SignedUpAt)
	}
	out := make([]SavedPaymentMethod, 0, count)
	for i := 0; i < count; i++ {
		// Method: era-appropriate online-channel payment vocabulary.
		method := pickSavedMethodForYear(signupYear, r)
		token := randomToken(c.CustomerID, i, r)
		var last4 *string
		var expMonth *int
		var expYear *int
		if methodHasCard(method) {
			s := fmt.Sprintf("%04d", r.IntN(10000))
			last4 = &s
			m := 1 + r.IntN(12)
			expMonth = &m
			y := signup.Year() + 1 + r.IntN(5)
			expYear = &y
		}
		// Added 30+ days after signup, before asOf.
		earliest := signup.AddDate(0, 0, 30)
		if earliest.After(asOf) {
			earliest = signup
		}
		span := asOf.Sub(earliest)
		var addedAt time.Time
		if span <= 0 {
			addedAt = earliest
		} else {
			addedAt = earliest.Add(time.Duration(r.Float64() * float64(span)))
		}
		ctr.savedPay++
		out = append(out, SavedPaymentMethod{
			SavedPaymentID: ctr.savedPay,
			Method:         method,
			Token:          token,
			CardLast4:      last4,
			ExpiryMonth:    expMonth,
			ExpiryYear:     expYear,
			AddedAt:        addedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}
	return out
}

func pickSavedMethodForYear(year int, r *rand.Rand) string {
	// Online-channel methods that existed in `year`. Defaults bias
	// toward whatever was current.
	options := []string{"card_emv"}
	if year >= 2001 {
		options = append(options, "third_party_online")
	}
	if year >= 2011 {
		options = append(options, "card_contactless")
	}
	if year >= 2015 {
		options = append(options, "mobile_wallet_ios")
	}
	if year >= 2016 {
		options = append(options, "mobile_wallet_android")
	}
	if year >= 2017 {
		options = append(options, "bnpl")
	}
	return options[r.IntN(len(options))]
}

func methodHasCard(method string) bool {
	switch method {
	case "card_emv", "card_contactless":
		return true
	}
	return false
}

func randomToken(customerID int64, idx int, r *rand.Rand) string {
	// Token is a deterministic-ish 32-char hex digest of
	// (customer_id, idx, RNG draw) — guarantees uniqueness without
	// having to coordinate across customers.
	salt := fmt.Sprintf("%d:%d:%d", customerID, idx, r.Uint64())
	h := sha256.Sum256([]byte(salt))
	return hex.EncodeToString(h[:])[:32]
}

// signupYearFor parses the year out of an ISO timestamp string.
func signupYearFor(signedUpAt string) int {
	if len(signedUpAt) < 4 {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, signedUpAt)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05Z", signedUpAt)
		if err != nil {
			// Fall back to year-substring parse.
			y, _ := time.Parse("2006", signedUpAt[:4])
			return y.Year()
		}
	}
	return t.Year()
}
