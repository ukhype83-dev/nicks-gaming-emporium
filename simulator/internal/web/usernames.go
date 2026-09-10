// Username generation (design §2): era-fashioned handles, including the
// firstname_year style that is anomaly A5 — the PII leak. Uniqueness is the
// caller's job (a global set across the whole account population); MakeUnique
// applies the digit-suffix convention real sites use.
package web

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

// UsernameInput is what the engine may draw on for one account.
type UsernameInput struct {
	FirstName   string // customers.first_name; may be "" (never captured)
	BirthYear   int    // 0 if unknown
	SignupYear  int    // account creation year (styles the handle)
	IsTroll     bool   // trolls sometimes pick a flavored handle
}

// leakRate: share of accounts using the FirstnameYYYY style when a first
// name is available (design §2: ~28%). Of those, ~80% use the real birth
// year — the leak — else the signup year.
const (
	leakRate      = 0.28
	birthYearRate = 0.80
)

// GenerateUsername builds one handle (pre-uniqueness).
func GenerateUsername(r *rand.Rand, b *Banks, in UsernameInput) string {
	if in.IsTroll && r.Float64() < 0.25 {
		if v := b.userparts["troll_flavor|any"]; len(v) > 0 {
			return v[r.IntN(len(v))]
		}
	}
	if in.FirstName != "" && r.Float64() < leakRate {
		yr := in.SignupYear
		if in.BirthYear > 0 && r.Float64() < birthYearRate {
			yr = in.BirthYear
		}
		return sanitizeName(in.FirstName) + fmt.Sprintf("%d", yr)
	}
	switch {
	case in.SignupYear <= 2003:
		w := b.userparts["dialup|word"]
		name := w[r.IntN(len(w))]
		if r.Float64() < 0.25 { // some doubled-up dial-up handles
			name += w[r.IntN(len(w))]
		}
		return name
	case in.SignupYear <= 2008:
		core := b.userparts["xx_era|core"]
		c := core[r.IntN(len(core))]
		switch r.IntN(3) {
		case 0:
			return "xX" + c + "Xx"
		case 1:
			return "x" + c + "x"
		default:
			return fmt.Sprintf("%s%02d", c, r.IntN(100))
		}
	case in.SignupYear <= 2012:
		f := b.userparts["underscore|first"]
		s := b.userparts["underscore|second"]
		return f[r.IntN(len(f))] + "_" + s[r.IntN(len(s))]
	default:
		f := b.userparts["modern|first"]
		s := b.userparts["modern|second"]
		return f[r.IntN(len(f))] + s[r.IntN(len(s))]
	}
}

// MakeUnique resolves collisions with the digit-suffix convention real sites
// use (gamefan → gamefan2 → gamefan88 …) and records the winner in taken.
//
// baseCount tracks how many handles already sit on each base. The numeric
// suffix is drawn from a span that widens with that occupancy (~4× the crowd),
// so probe density stays low — amortised O(1) — AND the space is unbounded, so
// a base that is massively oversubscribed at 3T scale (a popular dial-up word
// wanted by hundreds of thousands of accounts) can never saturate.
//
// The previous version drew from a FIXED space of only ~10,009 names per base
// (base, base2–9, base00–99, base100–9999). Once a base filled — which happens
// with the tiny username banks at 3T (18 dial-up words, 14 xx-era cores shared
// across hundreds of thousands of accounts) — taken[name] was permanently true
// and the loop spun forever. That infinite loop is what hung the 3T web load in
// phase 1 (flat RSS, 120% CPU, zero rows after 17 h).
//
// r is a per-customer derived stream, so the variable probe count is local to
// one account and never perturbs another customer's RNG; the only shared state
// (taken, baseCount) is claimed in fixed customer_id order, so the result is
// fully deterministic.
func MakeUnique(r *rand.Rand, taken map[string]bool, baseCount map[string]int, base string) string {
	if !taken[base] {
		taken[base] = true
		baseCount[base] = 1
		return base
	}
	c := baseCount[base] // handles already parked on this base (≥1)
	span := 8 + 4*c      // unbounded, ~4× the crowd → ~75% of draws land free
	for tries := 0; ; tries++ {
		name := fmt.Sprintf("%s%d", base, 2+r.IntN(span))
		if !taken[name] {
			taken[name] = true
			baseCount[base] = c + 1
			return name
		}
		// Widen if we hit an unexpectedly dense span — e.g. a resumed load
		// where baseCount was rebuilt empty but taken was seeded. Guarantees
		// termination for any taken/baseCount state; never triggers when
		// baseCount is accurate (draws land free in ~1-2 tries).
		if tries > 0 && tries%8 == 0 {
			span *= 2
		}
	}
}

// sanitizeName maps a legal first name to handle form: ASCII-ish, no
// spaces/hyphens, title case preserved ("Paul" → Paul1982).
func sanitizeName(s string) string {
	var sb strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			sb.WriteRune(c)
		}
	}
	if sb.Len() == 0 {
		return "user"
	}
	return sb.String()
}
