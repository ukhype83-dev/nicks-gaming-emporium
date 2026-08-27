// PostgreSQL Writer — the second backend behind the Writer interface (SQL
// Server being the first). The same deterministic generator loads byte-
// identical rows into either engine; only this thin adapter differs.
//
// Bulk loads use pgx v5's COPY protocol (pgx.CopyFrom), the Postgres analogue
// of SQL Server's TDS bulk-copy. On Postgres, COPY is the right path for both
// BulkInsert and BulkCopy, so they share one implementation.
//
// Boolean columns: the row converters emit Go bool for BIT/BOOLEAN fields
// (boolBit / boolToBit), which pgx maps straight onto a Postgres BOOLEAN.
// Decimal columns receive Go float64; the target column's scale rounds away
// any float representation artefact on insert (e.g. a DECIMAL(12,2) money
// column rounds 19.98999… to 19.99).
package dbwriter

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is a PostgreSQL Writer.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres opens and pings a pgx connection pool. DSN format:
//
//	postgres://user:password@host:port/dbname
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

// Close releases the connection pool.
func (p *Postgres) Close() error {
	p.pool.Close()
	return nil
}

// copyRows loads rows into schema.table via COPY. One COPY is a single
// atomic operation, so a failed call leaves the table unchanged — which is
// what makes whole-call retry (and the resume watermark) safe.
func (p *Postgres) copyRows(ctx context.Context, schema, table string, columns []string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	if len(columns) == 0 {
		return fmt.Errorf("no columns for %s.%s", schema, table)
	}
	n, err := p.pool.CopyFrom(ctx, pgx.Identifier{schema, table}, columns, pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("copy into %s.%s (%d rows): %w", schema, table, len(rows), err)
	}
	if int(n) != len(rows) {
		return fmt.Errorf("copy into %s.%s: expected %d rows, wrote %d", schema, table, len(rows), n)
	}
	return nil
}

// BulkInsert loads rows via COPY (Postgres has no cheaper small-batch path
// worth special-casing).
func (p *Postgres) BulkInsert(ctx context.Context, schema, table string, columns []string, rows [][]any) error {
	return p.copyRows(ctx, schema, table, columns, rows)
}

// BulkCopy loads rows via COPY — the native bulk protocol.
func (p *Postgres) BulkCopy(ctx context.Context, schema, table string, columns []string, rows [][]any) error {
	return p.copyRows(ctx, schema, table, columns, rows)
}

// MaxBigint returns the largest value of column in schema.table (0 when
// empty), cast to bigint so it works for int/smallint columns too.
func (p *Postgres) MaxBigint(ctx context.Context, schema, table, column string) (int64, error) {
	stmt := fmt.Sprintf("SELECT COALESCE(MAX(%s), 0)::bigint FROM %s.%s", column, schema, table)
	var n int64
	if err := p.pool.QueryRow(ctx, stmt).Scan(&n); err != nil {
		return 0, fmt.Errorf("max(%s) from %s.%s: %w", column, schema, table, err)
	}
	return n, nil
}

// DeleteAbove removes rows where column > threshold (resume cleanup).
func (p *Postgres) DeleteAbove(ctx context.Context, schema, table, column string, threshold int64) error {
	stmt := fmt.Sprintf("DELETE FROM %s.%s WHERE %s > $1", schema, table, column)
	if _, err := p.pool.Exec(ctx, stmt, threshold); err != nil {
		return fmt.Errorf("delete above %d from %s.%s: %w", threshold, schema, table, err)
	}
	return nil
}

// ExecSQL runs a single DML/DDL statement (e.g. the monthly_summary roll-up).
func (p *Postgres) ExecSQL(ctx context.Context, stmt string) error {
	if _, err := p.pool.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("ExecSQL: %w", err)
	}
	return nil
}

// InitSchemaPostgres applies a .sql file to the target database. Unlike SQL
// Server there are no GO batch separators — a Postgres schema file is plain
// semicolon-separated statements, which pgx's simple protocol runs in one
// Exec (as a single implicit transaction).
func InitSchemaPostgres(ctx context.Context, dsn, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read schema file: %w", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, string(content)); err != nil {
		return fmt.Errorf("apply schema %s: %w", path, err)
	}
	return nil
}

// QueryToStdoutPostgres runs one query and prints the result as TSV (header +
// rows) to stdout — the MCP-independent validation path, mirroring the SQL
// Server QueryToStdout.
func QueryToStdoutPostgres(ctx context.Context, dsn, query string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = string(f.Name)
	}
	fmt.Println(strings.Join(cols, "\t"))

	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return err
		}
		cells := make([]string, len(vals))
		for i, v := range vals {
			cells[i] = pgCellToString(v)
		}
		fmt.Println(strings.Join(cells, "\t"))
	}
	return rows.Err()
}

// RunETLPostgres executes one autocommit SQL statement (e.g.
// "CALL batch.usp_refresh_everything(true)") via the SIMPLE query protocol.
// Unlike InitSchemaPostgres (extended protocol, one implicit transaction), the
// simple protocol runs in autocommit, so a CALLed procedure may COMMIT per
// month — the whole point of the bounded fact-load loop. Routing the ETL
// through InitSchemaPostgres instead would wrap it in an implicit transaction
// and the procedure's COMMIT would raise "invalid transaction termination".
func RunETLPostgres(ctx context.Context, dsn, sql string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.PgConn().Exec(ctx, sql).ReadAll(); err != nil {
		return fmt.Errorf("run ETL (%s): %w", sql, err)
	}
	return nil
}

// QueryStatusPostgres runs a validation query returning a single row whose
// first two columns are (status, detail), mirroring QueryStatus for the pgx
// backend. Used by the Postgres reconciliation gate.
func QueryStatusPostgres(ctx context.Context, dsn, query string) (status, detail string, err error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return "", "", fmt.Errorf("connect postgres: %w", err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, query)
	if err != nil {
		return "", "", fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", "", err
		}
		return "", "", fmt.Errorf("validation query returned no rows")
	}
	vals, err := rows.Values()
	if err != nil {
		return "", "", err
	}
	if len(vals) < 2 {
		return "", "", fmt.Errorf("validation query returned %d columns, want >=2", len(vals))
	}
	return pgCellToString(vals[0]), pgCellToString(vals[1]), nil
}

// pgCellToString renders a scanned Postgres cell. pgx decodes NUMERIC columns
// to pgtype.Numeric (a big.Int mantissa + base-10 exponent), which the shared
// cellToString would print as its raw struct — so format those as a decimal
// string here; everything else falls through to cellToString.
func pgCellToString(v any) string {
	if n, ok := v.(pgtype.Numeric); ok {
		return formatPgNumeric(n)
	}
	return cellToString(v)
}

// formatPgNumeric renders a pgtype.Numeric as a plain decimal string
// (mantissa Int scaled by 10^Exp), placing the decimal point for negative
// exponents.
func formatPgNumeric(n pgtype.Numeric) string {
	if !n.Valid {
		return "NULL"
	}
	if n.NaN {
		return "NaN"
	}
	if n.Int == nil {
		return "0"
	}
	neg := n.Int.Sign() < 0
	digits := new(big.Int).Abs(n.Int).String()
	exp := int(n.Exp)
	var out string
	switch {
	case exp >= 0:
		out = digits + strings.Repeat("0", exp)
	default:
		scale := -exp
		if len(digits) <= scale {
			digits = strings.Repeat("0", scale-len(digits)+1) + digits
		}
		point := len(digits) - scale
		out = digits[:point] + "." + digits[point:]
	}
	if neg {
		out = "-" + out
	}
	return out
}
