// Package customers generates customer records deterministically per
// §9.10, §9.12, and §9.12.7.
//
// Scale ranges from 500K (3 GB tier) to 250M (3 TB tier), so this is
// the first simulator module that must stream rather than accumulate.
// The public API is a callback-driven Generate() — same guarantees as
// shops (deterministic, seed-namespaced), just emits one customer at
// a time.
//
// Current scope:
//   - Customer + one current billing address (1:1 for V1 skeleton)
//   - Per-country allocation via policy.CustomerShares
//   - Signup dates distributed across policy.SignupEras, with non-US
//     customers clamped to NGE's 1999 international-expansion start
//   - Governing regime derived from policy.CustomerShares
//   - Plausible name from internal/names banks
//   - DOB sampled so customer age at signup is 15–75 (median ~35)
//   - Real postcode / city / lat-lng from GeoNames seed data
//   - Locale-aware synthetic street lines (reuses geography package)
//   - address_hash SHA-256 over normalised line1|postal|country
//   - source_system + signed_up_precision tagged per §9.10
//
// Deferred to V1.1: address history with effective_to supersession
// (most customers would have 1–3 addresses over time per §9.12.3),
// shipping-vs-billing distinction, email/phone/loyalty, anonymisation
// events.
package customers

import (
	"container/heap"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"emporium/internal/geography"
	"emporium/internal/names"
	"emporium/internal/policy"
	"emporium/internal/rng"
)

// Customer maps to dbo.customers in the V1 schema, with the
// customer's current billing address embedded for the JSON-lines
// skeleton output. The DB writer splits the nested Address (and the
// V1.6.0 engagement collections) into their respective tables on
// insert.
type Customer struct {
	CustomerID             int64           `json:"customer_id"`
	Status                 string          `json:"status"`
	SignedUpAt             string          `json:"signed_up_at"`
	// V1.14.5: SignedUpPrecision column dropped (was an obvious
	// simulator-tells artifact). Internal coarsening still happens —
	// pre-1995 timestamps round to YYYY-01-01, etc. — so the stored
	// timestamp's precision is implicit in the trailing zeros.
	GoverningRegime        string          `json:"governing_regime"`
	FirstName              *string         `json:"first_name"`
	LastName               *string         `json:"last_name"`
	DateOfBirth            *string         `json:"date_of_birth"`
	CountryCode            string          `json:"country_code"` // not in schema; kept for address-generation context
	SourceSystem           string          `json:"source_system"`
	AnonymisedAt           *string         `json:"anonymised_at"`
	DataRetentionExpiresAt *string         `json:"data_retention_expires_at"`
	Address                CustomerAddress `json:"address"`

	// Engagement collections (V1.6.0). Empty slices for the JSON path
	// where they aren't generated; the DB writer handles missing data.
	LifecycleEvents    []CustomerLifecycleEvent  `json:"lifecycle_events,omitempty"`
	Emails             []CustomerEmail           `json:"emails,omitempty"`
	CommPrefs          []CommunicationPreference `json:"comm_prefs,omitempty"`
	ConsentEvents      []ConsentEvent            `json:"consent_events,omitempty"`
	LoyaltyMemberships []LoyaltyMembership       `json:"loyalty_memberships,omitempty"`
	SavedPayments      []SavedPaymentMethod      `json:"saved_payments,omitempty"`
}

// CustomerAddress mirrors dbo.customer_addresses. In V1 skeleton
// every customer gets exactly one row with effective_to = NULL
// (current). V1.1 will add move history.
type CustomerAddress struct {
	CustomerAddressID int64   `json:"customer_address_id"`
	AddressType       string  `json:"address_type"`   // 'billing' for V1
	EffectiveFrom     string  `json:"effective_from"` // ISO date, = signup date
	EffectiveTo       *string `json:"effective_to"`   // always NULL in V1
	Line1             string  `json:"line1"`
	Line2             string  `json:"line2"`
	City              string  `json:"city"`
	Region            string  `json:"region"`
	PostalCode        string  `json:"postal_code"`
	CountryCode       string  `json:"country_code"`
	Latitude          float64 `json:"latitude"`
	Longitude         float64 `json:"longitude"`
	AddressHash       string  `json:"address_hash"`
	AnonymisedAt      *string `json:"anonymised_at"`
	SourceSystem      string  `json:"source_system"`
}

// Index is the per-country pool of generated customer_ids, used by
// transactions.Generate (V1.14.0) to attach a customer_id to a fraction
// of transactions per CustomerLinkageProbability. Each country's slice
// is sorted by signup_date ascending (so a transaction at date T can
// pick from `ids[:monthCount[T's prev month]]` — only customers who
// existed by then). Memory ≈ 8 bytes × total customer count.
type Index struct {
	byCountry map[string]*countryPool

	// V1.18.0 — saved-payment companion data. One packed ref per
	// customer who has at least one stored payment method (first
	// method only — the one customers actually reuse). Appended in
	// emission order; customer_id is globally ascending in that order,
	// so the slice is sorted and SavedPaymentFor can binary-search.
	// Memory: 24 bytes per ref; ~30% of post-2007 signups qualify
	// (~2.1 GB at 3T, budgeted in PROJECT_NOTES §10.37).
	savedPay []savedPayRef
}

// savedPayRef packs the lookup-relevant fields of a customer's FIRST
// saved payment method.
type savedPayRef struct {
	customerID int64
	firstID    int64
	addedDay   int32 // unix days (UTC) when the method was stored
	method     uint8 // savedMethodEnum value
}

// savedMethodEnum maps the six saveable methods to compact codes.
// Kept in sync with engagement.go pickSavedMethodForYear.
var savedMethodEnum = map[string]uint8{
	"card_emv":              1,
	"third_party_online":    2,
	"card_contactless":      3,
	"mobile_wallet_ios":     4,
	"mobile_wallet_android": 5,
	"bnpl":                  6,
}

// SavedPaymentFor returns the customer's first saved payment method id
// when (a) the customer has one, (b) its method string matches, and
// (c) it had been added by `at`. Used by transactions (V1.18.0) to
// attach payments.saved_payment_id. Read-only; safe under concurrent
// transaction workers.
func (idx *Index) SavedPaymentFor(customerID int64, at time.Time, method string) (int64, bool) {
	if idx == nil || len(idx.savedPay) == 0 {
		return 0, false
	}
	m, ok := savedMethodEnum[method]
	if !ok {
		return 0, false
	}
	i := sort.Search(len(idx.savedPay), func(i int) bool {
		return idx.savedPay[i].customerID >= customerID
	})
	if i >= len(idx.savedPay) || idx.savedPay[i].customerID != customerID {
		return 0, false
	}
	ref := idx.savedPay[i]
	if ref.method != m {
		return 0, false
	}
	// V1.18.1: available strictly from the day AFTER added_at. The
	// index is day-granular; allowing same-day use let a payment at
	// 04:51 reference a card "added" at 12:36 the same day (7 such
	// inversions at 30g). Next-day availability is conservative and
	// time-coherent at any intra-day ordering.
	if int32(at.Unix()/86400) <= ref.addedDay {
		return 0, false
	}
	return ref.firstID, true
}

type countryPool struct {
	ids        []int64     // customer_ids sorted by signup_date asc
	monthCount map[int]int // year*100+month → cumulative count of ids signed up by end of month (V1.14.1: was map[string]int)
}

// monthKey collapses a Go time.Time into a compact int we can use as a
// map key without allocating. Format: year*100 + month, so 2024-03 ==
// 202403. Avoids the ~24-byte/call allocation that Format("2006-01")
// did, which at 3T scale was ~10 GB of transient garbage across the
// transactions-generation phase.
func monthKey(t time.Time) int {
	return t.Year()*100 + int(t.Month())
}

// prevMonthKey returns the int key for the calendar month immediately
// before t — used as Pick's conservative cutoff so a transaction on
// 2024-03-15 draws from customers signed up by end of 2024-02.
func prevMonthKey(t time.Time) int {
	y, m := t.Year(), int(t.Month())
	m--
	if m == 0 {
		m = 12
		y--
	}
	return y*100 + m
}

// Pick returns a uniformly-random customer_id from `country` whose
// signup_date is on or before `asOf`. Returns (0, false) if no eligible
// customer exists. Uses month-resolution cutoff (one month conservative
// margin) to keep the lookup table small — a transaction at 2024-03-15
// draws from customers signed up by end of 2024-02. Off by ~30 days at
// the boundary, immaterial for realism.
func (idx *Index) Pick(country string, asOf time.Time, r *rand.Rand) (int64, bool) {
	if idx == nil {
		return 0, false
	}
	pool, ok := idx.byCountry[country]
	if !ok || len(pool.ids) == 0 {
		return 0, false
	}
	eligible := pool.monthCount[prevMonthKey(asOf)]
	if eligible == 0 {
		return 0, false
	}
	return pool.ids[r.IntN(eligible)], true
}

// Generate produces the customer estate for the given tier and streams
// each customer through emit(). Deterministic: same (tier, seed, asOf,
// postal-code TSV) yields byte-identical customer records. Returns the
// per-country customer Index for downstream transaction generation.
func Generate(tier string, seed uint64, asOf time.Time, postals *geography.Index, emit func(Customer)) (int, *Index, error) {
	total, ok := policy.CustomerCountByTier[tier]
	if !ok {
		return 0, nil, fmt.Errorf("unknown tier %q (expected 3g|30g|300g|3t)", tier)
	}

	allocations := allocatePerCountry(total)

	// Validate postal coverage before doing anything expensive.
	for _, share := range policy.CustomerShares {
		if allocations[share.Country] == 0 {
			continue
		}
		if postals.CountFor(share.Country) == 0 {
			return 0, nil, fmt.Errorf("no postal-code data for %s (expected in seed_data/postal_codes.tsv)", share.Country)
		}
	}

	// V1.14.1: streaming sampler. Previously we materialised one giant
	// []pending slice (3 × 56 bytes per customer = ~14 GB at 3T) and
	// global-sorted it. Now we sample per-country, sort each country's
	// slice in-place (24 bytes per customer = ~6 GB at 3T peak), then
	// merge-stream globally via a small heap. Customer_id assignment
	// is identical to the old global-sort order for every realistic
	// case — within-country signup ties at nanosecond resolution don't
	// happen in practice (Float64 × span gives ~10^16 distinct
	// timestamps per era).
	type pending struct {
		country string
		regime  string
		signup  time.Time
	}
	countrySignups := make(map[string][]time.Time, len(policy.CustomerShares))
	for _, share := range policy.CustomerShares {
		n := allocations[share.Country]
		if n == 0 {
			continue
		}
		dateRNG := rng.Derive(seed, "customers/signup/"+share.Country)
		earliestSignup := policy.USFirstEraStart
		if share.Country != "US" {
			earliestSignup = policy.InternationalMarketEntry
		}
		s := make([]time.Time, n)
		for i := 0; i < n; i++ {
			s[i] = sampleSignupDate(dateRNG, earliestSignup, asOf)
		}
		sort.Slice(s, func(i, j int) bool { return s[i].Before(s[j]) })
		countrySignups[share.Country] = s
	}

	// Per-country RNG streams for independent name / DOB / address
	// generation. Namespacing them means changing upstream generation
	// order doesn't shuffle downstream streams.
	nameRNG := make(map[string]*rand.Rand, len(policy.CustomerShares))
	dobRNG := make(map[string]*rand.Rand, len(policy.CustomerShares))
	postalRNG := make(map[string]*rand.Rand, len(policy.CustomerShares))
	streetRNG := make(map[string]*rand.Rand, len(policy.CustomerShares))
	engagementRNG := make(map[string]*rand.Rand, len(policy.CustomerShares))
	onlineRNG := make(map[string]*rand.Rand, len(policy.CustomerShares))
	// V1.14.5: per-country stream for "is this customer flagged for
	// address-hash dedup?" — only ~10% are, mimicking the real
	// dedup-flag pattern instead of a synthetic 100% SHA-256 on every row.
	hashFlagRNG := make(map[string]*rand.Rand, len(policy.CustomerShares))
	for _, share := range policy.CustomerShares {
		nameRNG[share.Country] = rng.Derive(seed, "customers/names/"+share.Country)
		dobRNG[share.Country] = rng.Derive(seed, "customers/dob/"+share.Country)
		postalRNG[share.Country] = rng.Derive(seed, "customers/postal/"+share.Country)
		streetRNG[share.Country] = rng.Derive(seed, "customers/street/"+share.Country)
		engagementRNG[share.Country] = rng.Derive(seed, "customers/engagement/"+share.Country)
		onlineRNG[share.Country] = rng.Derive(seed, "customers/online/"+share.Country)
		hashFlagRNG[share.Country] = rng.Derive(seed, "customers/hashflag/"+share.Country)
	}

	// Engagement-table ID counters — one monotonic stream per table,
	// shared across all customers in this Generate run.
	var ctr engagementCounters

	// V1.14.0: build per-country customer Index for transactions.Generate.
	// Pre-allocate ids slices using per-country counts; monthCount built
	// during the loop (cumulative-by-month per country).
	custIndex := &Index{byCountry: make(map[string]*countryPool, len(allocations))}
	for cc, n := range allocations {
		if n == 0 {
			continue
		}
		custIndex.byCountry[cc] = &countryPool{
			ids:        make([]int64, 0, n),
			monthCount: make(map[int]int, 480),
		}
	}

	// V1.14.1: heap-merge per-country sorted slices instead of iterating
	// a single global-sorted []pending. Tiebreak by country alphabetical
	// preserves the original sort.Slice(pendings) ordering, so customer_id
	// assignment is identical for non-tied signups (i.e. always in
	// practice).
	cursors := newSignupHeap(policy.CustomerShares, countrySignups)

	// V1.14.5: deterministic per-merge-position roll for customer_id gaps.
	// Real OLTP tables have ~1-2% identity-sequence holes from rolled-back
	// transactions, deletes that fully removed a row, and IDENTITY caches
	// that lost a contiguous range on instance restart. We mimic that by
	// skipping ~1.5% of slots — the customer at that position is never
	// emitted, but `idx` advances anyway so the next real customer's ID
	// is the one AFTER the gap.
	idGapRNG := rng.Derive(seed, "customers/id_gap")
	const customerIDGapRate = 0.015

	idx := 0
	emitted := 0
	for cursors.Len() > 0 {
		cur := cursors.peek()
		p := pending{
			country: cur.country,
			regime:  cur.regime,
			signup:  cur.times[cur.pos],
		}
		// Advance / pop AFTER reading; the closure below uses `p`.
		cur.pos++
		if cur.pos >= len(cur.times) {
			heap.Pop(cursors)
			cur.times = nil // free the per-country slice eagerly
		} else {
			heap.Fix(cursors, 0)
		}

		// V1.14.5: ~1.5% of slots are gaps — advance idx but skip the
		// customer. Roll BEFORE generation so we don't waste downstream RNG.
		if idGapRNG.Float64() < customerIDGapRate {
			idx++
			continue
		}

		first, last := names.Pick(p.country, nameRNG[p.country])
		dob := sampleDOB(p.signup, dobRNG[p.country])
		precision := policy.SignupPrecisionForYear(p.signup.Year())
		sourceSys := sourceSystemForSignupYear(p.signup.Year())
		// Coarsen once so both the customer timestamp and the address
		// effective-from reference the same recorded event.
		recordedSignup := coarsenToPrecision(p.signup, precision)
		signedUpStr := formatTimestamp(recordedSignup, precision)

		// V1.13.1: era-aware proximity. Customers acquired in the online
		// era can plausibly live anywhere in their country — they don't
		// need to be near a shop. Roll per-customer based on signup year.
		// V1.23.1: the walk-in branch now samples against the shop
		// estate AS OF the signup year (SampleEra), not the lifetime
		// footprint — a 1991 loyalty customer lives near a 1991 shop.
		var pc geography.PostalCode
		if onlineRNG[p.country].Float64() < policy.OnlineEraCustomerFraction(p.signup.Year()) {
			pc, _ = postals.SampleAnywhere(p.country, postalRNG[p.country])
		} else {
			pc, _ = postals.SampleEra(p.country, p.signup.Year(), postalRNG[p.country])
		}
		line1 := geography.GenerateStreetAddress(p.country, pc.City, streetRNG[p.country])
		addrID := int64(idx + 1)
		// V1.27: a localised apartment/unit line on ~30% of addresses in
		// unit locales — deterministic on the address id (no RNG draw).
		line2 := unitLine(p.country, addrID)
		// V1.27: populate address_hash for every row (a real dedup/match key
		// applies to the whole file, not a 10% sample). The per-country flag
		// RNG draw is retained so the downstream stream stays put.
		hashFlagRNG[p.country].Float64()
		addressHash := hashAddress(line1, line2, pc.Postal, p.country)
		addr := CustomerAddress{
			CustomerAddressID: addrID,
			AddressType:       "billing",
			EffectiveFrom:     recordedSignup.Format("2006-01-02"),
			EffectiveTo:       nil,
			Line1:             line1,
			Line2:             line2,
			City:              pc.City,
			Region:            pc.Region,
			PostalCode:        pc.Postal,
			CountryCode:       p.country,
			Latitude:          pc.Latitude,
			Longitude:         pc.Longitude,
			AddressHash:       addressHash,
			AnonymisedAt:      nil,
			SourceSystem:      sourceSys,
		}

		firstPtr := first
		lastPtr := last
		dobStr := dob.Format("2006-01-02")
		c := Customer{
			CustomerID:             int64(idx + 1),
			Status:                 "active",
			SignedUpAt:             signedUpStr,
			GoverningRegime:        p.regime,
			FirstName:              &firstPtr,
			LastName:               &lastPtr,
			DateOfBirth:            &dobStr,
			CountryCode:            p.country,
			SourceSystem:           sourceSys,
			AnonymisedAt:           nil,
			DataRetentionExpiresAt: nil,
			Address:                addr,
		}
		// Engagement: lifecycle, emails, comm prefs, consent, loyalty,
		// saved-payment all generated alongside the customer per §10.13.
		eng := engagementRNG[p.country]
		c.LifecycleEvents = generateLifecycleEvents(&c, &ctr, eng)
		c.Emails = generateEmails(&c, first, last, &ctr, eng)
		c.CommPrefs = generateCommPrefs(&c, len(c.Emails) > 0, &ctr, eng)
		c.ConsentEvents = generateConsentEvents(&c, c.CommPrefs, &ctr, eng)
		c.LoyaltyMemberships = generateLoyaltyMemberships(&c, &ctr, asOf, eng)
		c.SavedPayments = generateSavedPaymentMethods(&c, &ctr, asOf, eng)
		// V1.18.0: index the first saved method for transactions-phase
		// saved_payment_id attachment. Emission order = ascending
		// customer_id, so appends keep idx.savedPay binary-searchable.
		if len(c.SavedPayments) > 0 {
			sp := c.SavedPayments[0]
			if code, ok := savedMethodEnum[sp.Method]; ok {
				if added, err := time.Parse("2006-01-02T15:04:05.000Z", sp.AddedAt); err == nil {
					custIndex.savedPay = append(custIndex.savedPay, savedPayRef{
						customerID: c.CustomerID,
						firstID:    sp.SavedPaymentID,
						addedDay:   int32(added.Unix() / 86400),
						method:     code,
					})
				}
			}
		}
		// V1.14.0: feed the customer Index. Append by country (preserves
		// signup-date order since `pendings` is already sorted), and bump
		// the cumulative monthCount for this country's signup-month.
		// V1.14.1: int monthKey avoids the per-customer string allocation
		// from Format("2006-01") — at 3T that was ~6 GB of transient
		// garbage. monthKey is `year*100 + month`, identical lookup
		// semantics, zero allocations.
		pool := custIndex.byCountry[p.country]
		pool.ids = append(pool.ids, c.CustomerID)
		pool.monthCount[monthKey(recordedSignup)] = len(pool.ids)
		emit(c)
		idx++
		emitted++
	}

	// monthCount currently only records the cumulative count at months
	// where at least one customer signed up. Forward-fill so a lookup
	// for any later month resolves correctly even if zero customers
	// happened to sign up that month. V1.14.1: int keys here too.
	for _, pool := range custIndex.byCountry {
		keys := make([]int, 0, len(pool.monthCount))
		for k := range pool.monthCount {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		running := 0
		startYear := keys[0] / 100
		startMonth := keys[0] % 100
		endYear := asOf.Year()
		endMonth := int(asOf.Month())
		for y := startYear; y <= endYear; y++ {
			mStart := 1
			mEnd := 12
			if y == startYear {
				mStart = startMonth
			}
			if y == endYear {
				mEnd = endMonth
			}
			for m := mStart; m <= mEnd; m++ {
				k := y*100 + m
				if c, ok := pool.monthCount[k]; ok {
					running = c
				} else {
					pool.monthCount[k] = running
				}
			}
		}
	}

	return emitted, custIndex, nil
}

// signupCursor tracks one country's progress through its per-country
// sorted signup-date slice during the V1.14.1 heap-merge of all
// countries into a global (signup, country) order.
type signupCursor struct {
	country string
	regime  string
	times   []time.Time
	pos     int
}

// signupHeap is a min-heap over signupCursor pointers ordered by the
// cursor's current signup time, tiebroken by country alphabetical —
// exactly matching the global sort.Slice tiebreak the pre-V1.14.1
// pendings-flatten code used.
type signupHeap []*signupCursor

func (h signupHeap) Len() int { return len(h) }
func (h signupHeap) Less(i, j int) bool {
	ti := h[i].times[h[i].pos]
	tj := h[j].times[h[j].pos]
	if !ti.Equal(tj) {
		return ti.Before(tj)
	}
	return h[i].country < h[j].country
}
func (h signupHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *signupHeap) Push(x any)         { *h = append(*h, x.(*signupCursor)) }
func (h *signupHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
func (h *signupHeap) peek() *signupCursor { return (*h)[0] }

// newSignupHeap initializes a min-heap over per-country signup slices,
// iterated in policy.CustomerShares order so the country-string-pointer
// metadata stays in a deterministic order even with same-time ties.
func newSignupHeap(shares []policy.CountryCustomerShare, countrySignups map[string][]time.Time) *signupHeap {
	h := &signupHeap{}
	for _, share := range shares {
		times, ok := countrySignups[share.Country]
		if !ok || len(times) == 0 {
			continue
		}
		heap.Push(h, &signupCursor{
			country: share.Country,
			regime:  share.Regime,
			times:   times,
			pos:     0,
		})
	}
	return h
}

// hashAddress returns a stable SHA-256 of the normalised address. Used
// for dedupe / fraud queries and must survive anonymisation (the hash
// stays even when line1 is NULLed).
func hashAddress(line1, line2, postal, country string) string {
	normalised := strings.ToLower(strings.TrimSpace(line1)) + "|" +
		strings.ToLower(strings.TrimSpace(line2)) + "|" +
		strings.ToLower(strings.TrimSpace(postal)) + "|" +
		strings.ToLower(country)
	h := sha256.Sum256([]byte(normalised))
	return hex.EncodeToString(h[:])
}

// unitLocale maps countries that commonly carry an apartment/unit line to
// their local label. Confined to the number-first locales the address
// widener also targets.
var unitLocale = map[string]string{"US": "Apt", "CA": "Apt", "AU": "Apt", "GB": "Flat", "FR": "Appt"}

// unitLine returns a localised unit line for ~30% of addresses in unit
// locales (deterministic on the address id — no RNG draw); "" otherwise.
func unitLine(country string, addrID int64) string {
	label, ok := unitLocale[country]
	if !ok || addrID%10 >= 3 {
		return ""
	}
	return fmt.Sprintf("%s %d", label, (addrID%60)+1)
}

// allocatePerCountry distributes `total` customers across CustomerShares
// using largest-remainder rounding.
func allocatePerCountry(total int) map[string]int {
	alloc := make(map[string]int, len(policy.CustomerShares))
	type entry struct {
		country   string
		ideal     float64
		allocated int
	}
	entries := make([]entry, 0, len(policy.CustomerShares))
	usedUp := 0
	for _, s := range policy.CustomerShares {
		ideal := float64(total) * s.Share
		a := int(ideal)
		entries = append(entries, entry{s.Country, ideal, a})
		usedUp += a
	}
	leftover := total - usedUp
	sort.SliceStable(entries, func(i, j int) bool {
		fi := entries[i].ideal - float64(entries[i].allocated)
		fj := entries[j].ideal - float64(entries[j].allocated)
		return fi > fj
	})
	for k := 0; k < leftover && k < len(entries); k++ {
		entries[k].allocated++
	}
	for _, e := range entries {
		alloc[e.country] = e.allocated
	}
	return alloc
}

// sampleSignupDate picks a date per policy.SignupEras, rejection-
// sampled to be ≥ earliest (for non-US, 1999+) and ≤ asOf.
//
// V1.17.0: the signup clock is additionally capped at
// policy.SignupCutoff (liquidation, 2016-09-30). V1.15.0 capped the
// transaction and HR clocks but missed this one — at 3T, 51% of
// customers were "signing up" at a company that no longer existed
// (training-catalog audit finding F1). The archive's privacy events
// may still occur after the cutoff; new customers may not.
func sampleSignupDate(r *rand.Rand, earliest, asOf time.Time) time.Time {
	if asOf.After(policy.SignupCutoff) {
		asOf = policy.SignupCutoff
	}
	// Weighted era selection.
	u := r.Float64()
	cum := 0.0
	var era policy.SignupEra
	for _, e := range policy.SignupEras {
		cum += e.Share
		if u <= cum {
			era = e
			break
		}
	}
	// Clamp era range to [earliest, asOf].
	start := time.Date(era.StartYear, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(era.EndYear, time.December, 31, 23, 59, 59, 0, time.UTC)
	if start.Before(earliest) {
		start = earliest
	}
	if end.After(asOf) {
		end = asOf
	}
	if !end.After(start) {
		// Era falls entirely before earliest or after asOf.
		// Pick the closest valid date.
		return earliest
	}
	span := end.Sub(start)
	return start.Add(time.Duration(r.Float64() * float64(span)))
}

// sampleDOB produces a plausible date-of-birth. Age-at-signup is
// roughly normal around 35, clamped to 15–75.
func sampleDOB(signup time.Time, r *rand.Rand) time.Time {
	// Normal-ish: mean 35, spread via Box-Muller from two uniforms.
	u1, u2 := r.Float64(), r.Float64()
	if u1 < 1e-9 {
		u1 = 1e-9
	}
	z := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	age := 35.0 + z*12.0
	if age < 15 {
		age = 15
	}
	if age > 75 {
		age = 75
	}
	days := int(age * 365.25)
	return signup.AddDate(0, 0, -days)
}

// sourceSystemForSignupYear matches §9.10.1 but uses signup year.
// Pre-2004 signups lived in the legacy POS; 2004-2015 in the
// transitional system; 2016+ native to pos_current; plus web/mobile
// channels once they existed. For V1 skeleton we use the POS lineage
// since that's where customer records actually lived; web signups
// from 2001+ would re-key to web_legacy_2001_2007 in a future pass.
func sourceSystemForSignupYear(year int) string {
	switch {
	case year <= 2003:
		return "pos_legacy_1986_2003"
	case year <= 2015:
		return "pos_transitional_2004_2015"
	default:
		return "pos_current"
	}
}

// coarsenToPrecision returns t zero-filled below the given precision
// level. Used to produce a single "recorded signup" value that both
// the customers.signed_up_at column and the customer_addresses
// .effective_from column reference.
func coarsenToPrecision(t time.Time, precision string) time.Time {
	switch precision {
	case "year":
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case "hour":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
	case "minute":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	case "second":
		return t.UTC().Truncate(time.Second)
	default: // millisecond
		return t.UTC().Truncate(time.Millisecond)
	}
}

// formatTimestamp emits the ISO text for a given precision. Expects
// t to already be coarsened to the same precision.
func formatTimestamp(t time.Time, precision string) string {
	if precision == "millisecond" {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

