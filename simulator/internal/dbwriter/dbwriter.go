// Package dbwriter provides target-agnostic bulk-insert support for
// the simulator. Current implementations:
//
//   - MSSQL — SQL Server via github.com/microsoft/go-mssqldb and
//     mssql.CopyIn (uses the TDS bulk-copy protocol underneath)
//
// The Writer interface is deliberately minimal: the caller hands over
// batches of rows for a specific table. Schema creation is out of
// scope here — user runs schema_v1_sqlserver.sql against the target
// database before the simulator loads anything.
package dbwriter

import "context"

// Writer is the bulk-insert target interface. Implementations handle
// the database-specific COPY / BULK-INSERT path underneath.
//
// Each BulkInsert call is a single logical load of `rows` into
// `schema.table`, with columns in the given order. The implementation
// decides its own transaction semantics — for SQL Server this is a
// single mssql.CopyIn-backed transaction per call.
//
// MaxBigint and DeleteAbove support resumable loads: on restart the
// loader queries MAX(pk) per phase, deletes any partial rows above
// the safe resume point, and skips already-loaded rows from the
// generator. Because the simulator is deterministic and assigns PKs
// monotonically from 1, MAX(pk) is a sufficient checkpoint without
// any side-channel state.
type Writer interface {
	BulkInsert(ctx context.Context, schema, table string, columns []string, rows [][]any) error

	// BulkCopy loads rows via the database-native bulk protocol — for
	// SQL Server this is the TDS bulk-copy RPC (mssql.CopyIn).
	// Significantly faster than
	// BulkInsert on hot tables (>10K rows). Type contract per
	// implementation; for MSSQL each row element must be a concrete
	// scalar or untyped nil — pointer types are rejected.
	BulkCopy(ctx context.Context, schema, table string, columns []string, rows [][]any) error

	// MaxBigint returns the largest value of `column` in `schema.table`,
	// or 0 if the table is empty. The column must be an integer type;
	// implementations should CAST to BIGINT to handle INT/SMALLINT
	// columns uniformly.
	MaxBigint(ctx context.Context, schema, table, column string) (int64, error)

	// DeleteAbove removes rows from `schema.table` where `column` is
	// strictly greater than `threshold`. Used to clean up partial
	// rows above the resume watermark before re-running the generator.
	DeleteAbove(ctx context.Context, schema, table, column string, threshold int64) error

	// V1.15.0 — ExecSQL runs an arbitrary DML/DDL statement against
	// the target. Used for the post-load finance.monthly_summary
	// aggregation pass which can't be expressed as straight BulkInsert.
	ExecSQL(ctx context.Context, stmt string) error

	Close() error
}
