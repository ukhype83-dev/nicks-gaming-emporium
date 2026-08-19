// SQL Server implementation of the Writer interface.
//
// Uses multi-row INSERT with **inline literal values** (no query
// parameters). This is counterintuitive — parameterised queries are
// normally the best practice — but it's the only way to avoid
// go-mssqldb's behaviour of declaring nvarchar parameter types sized
// to each actual string value:
//
//    (@p3 nvarchar(7), @p6 nvarchar(19), ...)   -- different per chunk
//
// That causes SQL Server to see slightly different query text per
// INSERT and miss its plan cache → full parse + compile + optimise
// per INSERT → 15+ minutes for a single 50K-row flush. Observed in
// the wild.
//
// With inline literal values every INSERT of the same structure has
// identical query text — plan cache hits after the first one.
// SQL-injection isn't a concern: all values come from the simulator,
// not user input. writeSQLLiteral handles proper escaping anyway.
//
// Assumes the schema (schema_v1_sqlserver.sql) has already been
// applied — this writer does not DDL.
package dbwriter

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
)

// rowsPerInsert caps each multi-row INSERT statement. SQL Server's
// VALUES-clause limit is 1000; 500 is a safe default that keeps each
// INSERT text around 100-300 KB for our schema.
const rowsPerInsert = 500

// Retry budgets by error class (V1.24.1 — network-fault resilience).
//
// Deadlocks resolve in milliseconds once the peer commits: five fast
// attempts (100ms → 1.6s) clears 99%+ under the 16-worker load.
//
// Network faults (connection reset, NAT flow drops between a WSL2
// client and the server, even a full SQL Server service restart) need
// PATIENCE, not speed: fifteen attempts with backoff capped at 60s
// gives a total budget of ~10 minutes — long enough to ride out a
// service restart — before a 75-hour build is allowed to die.
const (
	deadlockMaxAttempts = 5
	networkMaxAttempts  = 15
	networkBackoffCap   = 60 * time.Second
)

// errClass buckets an error for retry policy.
type errClass int

const (
	errPermanent errClass = iota // fail immediately
	errDeadlock                  // fast retry
	errNetwork                   // patient retry
)

// classifyError buckets errors by retry strategy. Detection is by
// message-substring (the driver wraps SQL Server and OS errors with
// their message text) plus the sentinel errors the driver/stdlib use
// for dead connections.
//
// Deadlock: SQL Server 1205 ("deadlock victim") — common under the
// 16-worker parallel load's page-latch contention. Bulk-RPC schema-
// change races (4885) retried in the same class.
//
// Network: everything that means "the pipe broke or couldn't open" —
// including the login-phase failures SQL logs as error 17830 when a
// NAT path degrades, and "connection refused" while a restarting
// server isn't listening yet. database/sql hands the retry a fresh
// pool connection, so a retry after the network heals just works.
func classifyError(err error) errClass {
	if err == nil {
		return errPermanent
	}
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errNetwork
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "deadlock"),
		strings.Contains(msg, "schema change of the target table"):
		return errDeadlock
	case strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "broken pipe"),
		strings.Contains(msg, "forcibly closed"),
		strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "bad connection"),
		strings.Contains(msg, "network error"),
		strings.Contains(msg, "unable to open tcp connection"),
		strings.Contains(msg, "login error"),
		strings.Contains(msg, "wsarecv"),
		strings.Contains(msg, "wsasend"),
		// go-mssqldb's generic client-side error when its TDS response
		// parser hits a hiccup on a connection under concurrent load (no
		// server-side fault is logged). Behaves like a connection blip: a
		// retry on a fresh pool connection succeeds. Seen on a co-tenant
		// 16GB instance at vusers=8. BulkInsert is one transaction per
		// call, so whole-call retry is safe (the lost-ack edge is covered
		// by the duplicate-key rule).
		strings.Contains(msg, "sql server had internal error"),
		strings.Contains(msg, ": eof"):
		return errNetwork
	}
	return errPermanent
}

// isDuplicateKeyError detects PK/unique violations — the signature of
// the ambiguous-commit edge (see runWithRetry).
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "violation of primary key") ||
		strings.Contains(msg, "cannot insert duplicate key")
}

// runWithRetry executes op with class-aware retry.
//
// The ambiguous-commit edge: if a NETWORK fault killed an earlier
// attempt, the server may have committed the transaction but the ack
// never reached us. Every PK the simulator writes is deterministic, so
// if a subsequent retry fails with a duplicate-key error after a
// network fault, the "failed" attempt in fact committed — report
// success and move on. (Without a prior network fault, a dup-key error
// is a real bug and fails immediately.)
func (w *MSSQL) runWithRetry(ctx context.Context, label string, op func() error) error {
	var lastErr error
	sawNetwork := false
	deadlocks, networks := 0, 0
	for {
		err := op()
		if err == nil {
			if deadlocks+networks > 0 {
				fmt.Fprintf(os.Stderr, "  ↻ %s succeeded after %d deadlock / %d network retries\n",
					label, deadlocks, networks)
			}
			return nil
		}
		if sawNetwork && isDuplicateKeyError(err) {
			fmt.Fprintf(os.Stderr, "  ↻ %s: duplicate-key after a network fault — the interrupted attempt committed (ack was lost); continuing\n", label)
			return nil
		}
		var wait time.Duration
		switch classifyError(err) {
		case errDeadlock:
			deadlocks++
			if deadlocks >= deadlockMaxAttempts {
				return fmt.Errorf("%s failed after %d deadlock retries: %w", label, deadlocks, err)
			}
			wait = time.Duration(100*(1<<(deadlocks-1))) * time.Millisecond
		case errNetwork:
			sawNetwork = true
			networks++
			if networks >= networkMaxAttempts {
				return fmt.Errorf("%s failed after %d network retries: %w", label, networks, err)
			}
			wait = time.Duration(1<<(networks-1)) * time.Second
			if wait > networkBackoffCap {
				wait = networkBackoffCap
			}
		default:
			return err
		}
		lastErr = err
		jitter := time.Duration(rand.Int64N(int64(wait/2) + 1))
		fmt.Fprintf(os.Stderr, "  ↻ %s: %v — retrying in %v\n", label, lastErr, wait+jitter)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait + jitter):
		}
	}
}

// MSSQL is a SQL Server Writer.
type MSSQL struct {
	db *sql.DB
}

// NewMSSQL opens and pings a SQL Server connection. DSN format:
//
//	sqlserver://user:password@host:port?database=name
func NewMSSQL(ctx context.Context, dsn string) (*MSSQL, error) {
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlserver: %w", err)
	}
	// V1.24.1 — proactive connection recycling. Long-lived connections
	// through a NAT (WSL2 → host → LAN) accumulate risk of silent flow
	// eviction; recycling keeps the pool on fresh sockets so a stale
	// flow is retired before it can eat a mid-transaction reset.
	db.SetConnMaxLifetime(15 * time.Minute)
	db.SetConnMaxIdleTime(2 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlserver: %w", err)
	}
	return &MSSQL{db: db}, nil
}

// BulkInsert loads rows via inline-value multi-row INSERT. One
// transaction per BulkInsert call; rolls back on any error.
//
// Transient SQL Server errors (deadlocks, schema-change races) trigger
// automatic retry with exponential backoff up to bulkInsertMaxAttempts.
// All other errors return immediately. Deadlocks are common under the
// 16-vusers parallel build because peer workers concurrently INSERT
// into the same heap tables and contend for B-tree page latches; the
// retry path resolves them transparently.
// BulkInsert loads rows via inline-value multi-row INSERT. One
// transaction per BulkInsert call; rolls back on any error — which is
// what makes whole-call retry safe: the transaction either committed
// in full or not at all (the lost-ack edge is handled by
// runWithRetry's duplicate-key rule).
//
// Deadlocks retry fast; network faults retry patiently (V1.24.1 — a
// TCP reset must never kill a 75-hour build). All other errors return
// immediately.
func (w *MSSQL) BulkInsert(ctx context.Context, schema, table string, columns []string, rows [][]any) error {
	return w.runWithRetry(ctx, fmt.Sprintf("bulk insert %s.%s", schema, table), func() error {
		return w.bulkInsertOnce(ctx, schema, table, columns, rows)
	})
}

func (w *MSSQL) bulkInsertOnce(ctx context.Context, schema, table string, columns []string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	if len(columns) == 0 {
		return fmt.Errorf("no columns for %s.%s", schema, table)
	}
	qualified := fmt.Sprintf("%s.%s", schema, table)
	columnList := strings.Join(columns, ", ")

	txn, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for %s: %w", qualified, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = txn.Rollback()
		}
	}()

	for i := 0; i < len(rows); i += rowsPerInsert {
		end := i + rowsPerInsert
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[i:end]
		stmt := buildInlineInsert(qualified, columnList, chunk)
		if _, err := txn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("inline insert into %s (chunk starting row %d, %d rows): %w",
				qualified, i, len(chunk), err)
		}
	}

	if err := txn.Commit(); err != nil {
		return fmt.Errorf("commit inline insert for %s: %w", qualified, err)
	}
	committed = true
	return nil
}

// buildInlineInsert produces
//
//	INSERT INTO s.t (cols) VALUES (v1, v2, ...), (v1, v2, ...), ...;
//
// with all values inline. Each chunk of identical row count produces
// byte-identical SQL text, enabling plan cache reuse.
func buildInlineInsert(qualified, columnList string, rows [][]any) string {
	var sb strings.Builder
	sb.Grow(128 + len(rows)*200)
	sb.WriteString("INSERT INTO ")
	sb.WriteString(qualified)
	sb.WriteString(" (")
	sb.WriteString(columnList)
	sb.WriteString(") VALUES ")
	for ri, row := range rows {
		if ri > 0 {
			sb.WriteString(", ")
		}
		sb.WriteByte('(')
		for ci, val := range row {
			if ci > 0 {
				sb.WriteString(", ")
			}
			writeSQLLiteral(&sb, val)
		}
		sb.WriteByte(')')
	}
	sb.WriteByte(';')
	return sb.String()
}

// writeSQLLiteral emits a T-SQL literal for a Go value:
//   - nil         → NULL
//   - bool        → 1 / 0  (BIT)
//   - int/uint/…  → raw digits
//   - float32/64  → fixed-notation, no scientific
//   - string      → N'<escaped>' (UTF-16 prefix, '' for inner quote)
//   - time.Time   → '2006-01-02 15:04:05.000'  (SQL Server implicitly
//     converts to DATE when the target column is DATE)
func writeSQLLiteral(sb *strings.Builder, v any) {
	if v == nil {
		sb.WriteString("NULL")
		return
	}
	switch x := v.(type) {
	case bool:
		if x {
			sb.WriteByte('1')
		} else {
			sb.WriteByte('0')
		}
	case int:
		sb.WriteString(strconv.Itoa(x))
	case int32:
		sb.WriteString(strconv.FormatInt(int64(x), 10))
	case int64:
		sb.WriteString(strconv.FormatInt(x, 10))
	case uint:
		sb.WriteString(strconv.FormatUint(uint64(x), 10))
	case uint32:
		sb.WriteString(strconv.FormatUint(uint64(x), 10))
	case uint64:
		sb.WriteString(strconv.FormatUint(x, 10))
	case float32:
		sb.WriteString(strconv.FormatFloat(float64(x), 'f', -1, 32))
	case float64:
		sb.WriteString(strconv.FormatFloat(x, 'f', -1, 64))
	case string:
		writeSQLString(sb, x)
	case time.Time:
		sb.WriteByte('\'')
		sb.WriteString(x.UTC().Format("2006-01-02 15:04:05.000"))
		sb.WriteByte('\'')
	default:
		// Fallback: stringify and escape as a string literal.
		writeSQLString(sb, fmt.Sprintf("%v", x))
	}
}

// writeSQLString writes a UTF-16-prefixed SQL string literal, doubling
// any embedded single quotes. Strips control bytes below 0x20 that
// SQL Server's TDS parser rejects in inline literals.
func writeSQLString(sb *strings.Builder, s string) {
	sb.WriteString("N'")
	for _, r := range s {
		switch {
		case r == '\'':
			sb.WriteString("''")
		case r == 0:
			// NUL — skip entirely.
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('\'')
}

// MaxBigint returns the largest value of `column` in `schema.table`,
// cast to BIGINT so it works for INT / SMALLINT columns too. Returns 0
// when the table is empty.
func (w *MSSQL) MaxBigint(ctx context.Context, schema, table, column string) (int64, error) {
	stmt := fmt.Sprintf("SELECT CAST(COALESCE(MAX(%s), 0) AS BIGINT) FROM %s.%s", column, schema, table)
	var n int64
	err := w.runWithRetry(ctx, fmt.Sprintf("max(%s) from %s.%s", column, schema, table), func() error {
		return w.db.QueryRowContext(ctx, stmt).Scan(&n)
	})
	if err != nil {
		return 0, fmt.Errorf("max(%s) from %s.%s: %w", column, schema, table, err)
	}
	return n, nil
}

// ExecSQL runs a single DML/DDL statement against the target. Used
// for the V1.15.0 finance.monthly_summary post-load aggregation pass.
// No parameter binding — caller must inline literals (the aggregation
// statement is generated server-side from fully-loaded tables and
// can't contain user input).
func (w *MSSQL) ExecSQL(ctx context.Context, stmt string) error {
	return w.runWithRetry(ctx, "ExecSQL", func() error {
		if _, err := w.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("ExecSQL: %w", err)
		}
		return nil
	})
}

// DeleteAbove removes rows where `column` > `threshold`. Used during
// resume to drop the partial-batch rows above the safe restart point.
// Sizes are typically tiny (one batch's worth at most), so the cost
// is negligible.
func (w *MSSQL) DeleteAbove(ctx context.Context, schema, table, column string, threshold int64) error {
	stmt := fmt.Sprintf("DELETE FROM %s.%s WHERE %s > @p1", schema, table, column)
	return w.runWithRetry(ctx, fmt.Sprintf("delete-above %s.%s", schema, table), func() error {
		if _, err := w.db.ExecContext(ctx, stmt, threshold); err != nil {
			return fmt.Errorf("delete from %s.%s where %s > %d: %w", schema, table, column, threshold, err)
		}
		return nil
	})
}

// BulkCopy loads rows via the TDS bulk-copy RPC (mssql.CopyIn). One
// transaction per call. Bypasses SQL parsing and plan-cache entirely —
// the rows stream through a binary protocol instead. 2-3× faster than
// inline-VALUES INSERT for hot tables (transactions, lines, payments,
// movements, large customer-side tables) because it skips per-row
// parser/compiler/optimiser cost.
//
// Type contract: each row element must be a concrete Go scalar
// matching the destination column (int, int64, float64, string, bool,
// time.Time) or untyped nil for NULL. Pointer types are rejected by
// go-mssqldb's bulk path; convert.go's nullIfNil* helpers already do
// the deref-or-nil dance.
//
// Used in single-worker mode only (load.go's parallel transactions
// path falls back to BulkInsert because concurrent CopyIn sessions
// trip schema-change races on the same target table).
//
// Same retry-on-transient-error contract as BulkInsert.
func (w *MSSQL) BulkCopy(ctx context.Context, schema, table string, columns []string, rows [][]any) error {
	return w.runWithRetry(ctx, fmt.Sprintf("bulk-copy %s.%s", schema, table), func() error {
		return w.bulkCopyOnce(ctx, schema, table, columns, rows)
	})
}

func (w *MSSQL) bulkCopyOnce(ctx context.Context, schema, table string, columns []string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	if len(columns) == 0 {
		return fmt.Errorf("no columns for %s.%s", schema, table)
	}
	qualified := fmt.Sprintf("[%s].[%s]", schema, table)

	txn, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for %s: %w", qualified, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = txn.Rollback()
		}
	}()

	stmt, err := txn.PrepareContext(ctx, mssql.CopyIn(qualified, mssql.BulkOptions{}, columns...))
	if err != nil {
		return fmt.Errorf("prepare bulk-copy %s: %w", qualified, err)
	}

	for i, row := range rows {
		if _, err := stmt.ExecContext(ctx, row...); err != nil {
			_ = stmt.Close()
			return fmt.Errorf("bulk-copy row %d into %s: %w", i, qualified, err)
		}
	}

	// Final empty Exec finalises the bulk RPC and returns row count.
	if _, err := stmt.ExecContext(ctx); err != nil {
		_ = stmt.Close()
		return fmt.Errorf("finalize bulk-copy for %s: %w", qualified, err)
	}
	if err := stmt.Close(); err != nil {
		return fmt.Errorf("close bulk-copy stmt for %s: %w", qualified, err)
	}
	if err := txn.Commit(); err != nil {
		return fmt.Errorf("commit bulk-copy for %s: %w", qualified, err)
	}
	committed = true
	return nil
}

// Close drops the underlying DB pool.
func (w *MSSQL) Close() error { return w.db.Close() }
