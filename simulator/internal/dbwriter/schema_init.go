// Schema-init helpers. The SQL Server DDL file uses `GO` batch
// separators which only sqlcmd understands — database/sql / pgx will
// choke on them. SplitGOBatches() + ExecBatches() let the simulator
// apply the DDL directly via the mssql driver, removing the sqlcmd
// dependency.
package dbwriter

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// SplitGOBatches splits a SQL Server script on `GO` batch separators.
// A line that is exactly "GO" (case-insensitive, surrounding
// whitespace ignored) ends the current batch.
func SplitGOBatches(script string) []string {
	var batches []string
	var current strings.Builder
	for _, line := range strings.Split(script, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "GO") {
			if s := strings.TrimSpace(current.String()); s != "" {
				batches = append(batches, current.String())
			}
			current.Reset()
			continue
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	if s := strings.TrimSpace(current.String()); s != "" {
		batches = append(batches, current.String())
	}
	return batches
}

// InitSchema reads the SQL file at `path`, splits it on GO, and
// executes each batch sequentially against the supplied DSN. Intended
// for the simulator's `--init-schema` convenience flag.
//
// Hands out the same error format as a failed sqlcmd run would,
// including the 1-indexed batch number so the user can find the
// offending block.
func InitSchema(ctx context.Context, dsn, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read schema file: %w", err)
	}
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return fmt.Errorf("open sqlserver: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlserver: %w", err)
	}

	batches := SplitGOBatches(string(content))
	for i, batch := range batches {
		if strings.TrimSpace(batch) == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, batch); err != nil {
			return fmt.Errorf("schema batch %d failed: %w", i+1, err)
		}
	}
	return nil
}

// QueryToStdout runs a single query and prints the result set as TSV (header +
// rows) to stdout — a lightweight, MCP-independent way to validate loads and
// reconcile. NULLs print as "NULL"; []byte cells as strings.
func QueryToStdout(ctx context.Context, dsn, query string) error {
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return fmt.Errorf("open sqlserver: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlserver: %w", err)
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	fmt.Println(strings.Join(cols, "\t"))
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		cells := make([]string, len(cols))
		for i, v := range vals {
			cells[i] = cellToString(v)
		}
		fmt.Println(strings.Join(cells, "\t"))
	}
	return rows.Err()
}

// cellToString renders a scanned SQL cell as a display string: NULLs as
// "NULL", []byte as string, everything else via %v.
func cellToString(v any) string {
	switch tv := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(tv)
	default:
		return fmt.Sprintf("%v", tv)
	}
}

// QueryStatus runs a validation query expected to return a single row whose
// columns include "status" (PASS/FAIL) and "detail". It returns those two
// cells. This powers the --emit full reconciliation gate: each check is a
// self-contained query that computes its own verdict in SQL (it can see both
// sides of a comparison), so the Go side just collects the verdicts.
func QueryStatus(ctx context.Context, dsn, query string) (status, detail string, err error) {
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return "", "", fmt.Errorf("open sqlserver: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return "", "", fmt.Errorf("ping sqlserver: %w", err)
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return "", "", fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return "", "", err
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", "", err
		}
		return "", "", fmt.Errorf("validation query returned no rows")
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", "", err
	}
	m := make(map[string]string, len(cols))
	for i, c := range cols {
		m[strings.ToLower(c)] = cellToString(vals[i])
	}
	return m["status"], m["detail"], nil
}

// SetRecoverySimple flips the target database to SIMPLE recovery
// model. Combined with the Tablock: true bulk-copy option, this gives
// 10-50× faster loads since minimal-logging path is active.
//
// SIMPLE recovery means no point-in-time restore for this DB. That's
// fine for a benchmark / synthetic dataset — restore-from-backup isn't
// a workload we need to support.
//
// Call after InitSchema (or at any point after the DB exists).
func SetRecoverySimple(ctx context.Context, dsn string) error {
	dbName, err := databaseFromDSN(dsn)
	if err != nil {
		return fmt.Errorf("parse database from dsn: %w", err)
	}
	if dbName == "" {
		return fmt.Errorf("no database= in DSN; cannot determine target")
	}
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return fmt.Errorf("open sqlserver: %w", err)
	}
	defer db.Close()
	// ALTER DATABASE .. SET RECOVERY .. must run against master or the
	// target DB; it works from a connection whose default DB is the
	// target. We use a bracketed identifier to tolerate DB names with
	// unusual characters.
	stmt := fmt.Sprintf("ALTER DATABASE [%s] SET RECOVERY SIMPLE", strings.ReplaceAll(dbName, "]", "]]"))
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("set recovery simple: %w", err)
	}
	return nil
}

// databaseFromDSN extracts the database name from a SQL Server DSN
// like "sqlserver://user:pass@host:port?database=nge".
func databaseFromDSN(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	return u.Query().Get("database"), nil
}
