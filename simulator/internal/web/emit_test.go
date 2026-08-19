package web

import (
	"strings"
	"testing"
	"time"

	"emporium/internal/customers"
	"emporium/internal/geography"
)

const postalPath = "../../../seed_data/postal_codes.tsv"

// replayAccounts runs a real 3g customer population through the account
// emitter and returns the accounts plus the emitter.
func replayAccounts(t *testing.T, seed uint64) ([]Account, *Emitter, int) {
	t.Helper()
	postals, err := geography.Load(postalPath)
	if err != nil {
		t.Fatalf("load postals: %v", err)
	}
	banks := loadBanksT(t)
	e := NewEmitter(banks, seed, time.Date(2025, 4, 23, 0, 0, 0, 0, time.UTC), 1)

	var accounts []Account
	nCust := 0
	_, _, err = customers.Generate("3g", seed, e.asOf, postals, func(c customers.Customer) {
		nCust++
		if a, ok := e.AccountFor(c); ok {
			accounts = append(accounts, a)
		}
	})
	if err != nil {
		t.Fatalf("customers.Generate: %v", err)
	}
	return accounts, e, nCust
}

func TestAccountEmission(t *testing.T) {
	accounts, e, nCust := replayAccounts(t, 42)
	if len(accounts) == 0 {
		t.Fatal("no accounts emitted")
	}

	// Account rate should sit near the ~35% target (3g is small, so allow
	// a generous band).
	rate := float64(len(accounts)) / float64(nCust)
	if rate < 0.25 || rate > 0.45 {
		t.Errorf("account rate %.3f outside 0.25-0.45 (%d/%d)", rate, len(accounts), nCust)
	}

	seenUser := map[string]bool{}
	seenID := map[int64]bool{}
	leak := 0
	var lastID int64
	for _, a := range accounts {
		if seenUser[a.Username] {
			t.Fatalf("duplicate username %q", a.Username)
		}
		seenUser[a.Username] = true
		if seenID[a.AccountID] {
			t.Fatalf("duplicate account_id %d", a.AccountID)
		}
		seenID[a.AccountID] = true
		if a.AccountID <= lastID {
			t.Fatalf("account_id not ascending: %d after %d", a.AccountID, lastID)
		}
		lastID = a.AccountID

		if a.CreatedAt.Before(WebLaunch) || a.CreatedAt.After(SiteDark) {
			t.Errorf("account %d created_at %s outside web era", a.AccountID, a.CreatedAt)
		}
		if a.Username == "" || strings.ContainsAny(a.Username, " \t") {
			t.Errorf("bad username %q", a.Username)
		}
		switch a.Status {
		case "active", "closed", "banned":
		default:
			t.Errorf("bad status %q", a.Status)
		}
		switch a.SourceSystem {
		case "web_legacy_2001_2007", "web_2008_plus", "mobile_app_2010_plus":
		default:
			t.Errorf("bad source_system %q", a.SourceSystem)
		}
		// Count FirstnameYYYY leak handles (a capital letter run followed by
		// a 4-digit year, no separators — the A5 signature).
		if isLeakHandle(a.Username) {
			leak++
		}
	}

	// A5: a meaningful minority of handles leak name+year.
	if frac := float64(leak) / float64(len(accounts)); frac < 0.15 || frac > 0.40 {
		t.Errorf("A5 leak share %.3f outside 0.15-0.40 (%d/%d)", frac, leak, len(accounts))
	}

	// The customer→account map must be complete and consistent.
	if e.AccountCount() != len(accounts) {
		t.Errorf("map has %d, emitted %d", e.AccountCount(), len(accounts))
	}
	for _, a := range accounts {
		if id, ok := e.AccountID(a.CustomerID); !ok || id != a.AccountID {
			t.Errorf("map lookup for customer %d = (%d,%v), want %d", a.CustomerID, id, ok, a.AccountID)
		}
	}
}

func TestAccountDeterminism(t *testing.T) {
	a1, _, _ := replayAccounts(t, 42)
	a2, _, _ := replayAccounts(t, 42)
	if len(a1) != len(a2) {
		t.Fatalf("account count differs across runs: %d vs %d", len(a1), len(a2))
	}
	for i := range a1 {
		if a1[i] != a2[i] {
			t.Fatalf("account %d differs:\n%+v\n%+v", i, a1[i], a2[i])
		}
	}
}

// isLeakHandle detects the FirstnameYYYY pattern: letters then exactly a
// 19xx/20xx year at the end, nothing else.
func isLeakHandle(s string) bool {
	if len(s) < 5 {
		return false
	}
	yr := s[len(s)-4:]
	if yr[:2] != "19" && yr[:2] != "20" {
		return false
	}
	for _, c := range yr {
		if c < '0' || c > '9' {
			return false
		}
	}
	head := s[:len(s)-4]
	if head == "" {
		return false
	}
	for _, c := range head {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}
