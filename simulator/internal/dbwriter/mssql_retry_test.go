package dbwriter

import (
	"context"
	"errors"
	"testing"
)

// V1.24.1 — network-fault resilience. The exact failure that killed a
// 3T build at 40M rows must classify as retryable.
func TestClassifyError(t *testing.T) {
	cases := []struct {
		msg  string
		want errClass
	}{
		// The real one, verbatim from the failed build.
		{"inline insert into dbo.transactions (chunk starting row 2000, 500 rows): read tcp 10.0.0.23:58588->10.0.0.10:1433: read: connection reset by peer", errNetwork},
		{"login error: mssql: login error: timeout waiting for reply", errNetwork},
		{"unable to open tcp connection with host '10.0.0.10:1433': dial tcp: i/o timeout", errNetwork},
		{"dial tcp 10.0.0.10:1433: connect: connection refused", errNetwork},
		{"driver: bad connection", errNetwork},
		{"wsarecv: An existing connection was forcibly closed by the remote host.", errNetwork},
		// go-mssqldb client-side parser blip under concurrent load — no
		// server-side fault logged; a fresh-connection retry succeeds.
		// Verbatim from a co-tenant 30g build under concurrent load.
		{"inline insert into dbo.transaction_lines (chunk starting row 24500, 500 rows): SQL Server had internal error", errNetwork},
		{"Transaction (Process ID 87) was deadlocked on lock resources with another process and has been chosen as the deadlock victim.", errDeadlock},
		{"Bulk load failed due to schema change of the target table.", errDeadlock},
		{"Incorrect syntax near 'FRUM'.", errPermanent},
		{"Violation of PRIMARY KEY constraint 'PK__transactions'. Cannot insert duplicate key.", errPermanent},
	}
	for _, c := range cases {
		if got := classifyError(errors.New(c.msg)); got != c.want {
			t.Errorf("classifyError(%.60q) = %v, want %v", c.msg, got, c.want)
		}
	}
	if classifyError(nil) != errPermanent {
		t.Error("nil must not classify as retryable")
	}
}

// The ambiguous-commit rule: dup-key AFTER a network fault = the
// interrupted attempt committed → success. Dup-key with NO prior
// network fault = a real bug → immediate failure.
func TestRunWithRetryAmbiguousCommit(t *testing.T) {
	w := &MSSQL{}
	ctx := context.Background()

	// Network fault, then dup-key on the retry → success.
	calls := 0
	err := w.runWithRetry(ctx, "test", func() error {
		calls++
		if calls == 1 {
			return errors.New("read tcp: read: connection reset by peer")
		}
		return errors.New("Violation of PRIMARY KEY constraint 'PK_x'. Cannot insert duplicate key in object 'dbo.transactions'.")
	})
	if err != nil {
		t.Errorf("dup-key after network fault should be treated as committed, got: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 attempts, got %d", calls)
	}

	// Dup-key cold — a genuine bug — must fail immediately.
	calls = 0
	err = w.runWithRetry(ctx, "test", func() error {
		calls++
		return errors.New("Violation of PRIMARY KEY constraint 'PK_x'. Cannot insert duplicate key.")
	})
	if err == nil {
		t.Error("cold dup-key must fail")
	}
	if calls != 1 {
		t.Errorf("cold dup-key must not retry, got %d attempts", calls)
	}

	// Permanent errors fail immediately.
	calls = 0
	err = w.runWithRetry(ctx, "test", func() error {
		calls++
		return errors.New("Incorrect syntax near 'FRUM'.")
	})
	if err == nil || calls != 1 {
		t.Errorf("permanent error must fail on attempt 1 (calls=%d, err=%v)", calls, err)
	}
}

// Deadlocks retry fast and succeed when the contention clears.
func TestRunWithRetryDeadlock(t *testing.T) {
	w := &MSSQL{}
	calls := 0
	err := w.runWithRetry(context.Background(), "test", func() error {
		calls++
		if calls < 3 {
			return errors.New("chosen as the deadlock victim")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Errorf("deadlock retry: err=%v calls=%d", err, calls)
	}
}
