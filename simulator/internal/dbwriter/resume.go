// Resumable load support. The simulator is deterministic — same seed
// always emits the same rows in the same order with the same
// monotonically-assigned PKs starting at 1. So the data already in the
// target tables IS the checkpoint; no side-channel state required.
//
// On a fresh load all watermarks are 0 and the helpers here are
// no-ops. On a restart after partial load, each phase queries MAX(pk)
// across its tables, takes the MIN as the safe resume point, and
// DELETEs any partial rows above it. The generator then runs as
// normal but the loader's callback skips rows whose PK is at or below
// the watermark.
//
// The MIN-of-MAXes rule handles the multi-table flush case where a
// crash between BulkInsert calls leaves children behind their parents.
// Cleanup deletes are tiny (worst case one batch's worth) so the cost
// is negligible vs the protection it buys.
package dbwriter

import (
	"context"
	"fmt"
	"os"
)

// phaseTable describes one (schema, table, anchor-column) tuple
// participating in a multi-table phase. The anchor column is the
// shared parent PK or FK across all tables in the phase
// (e.g. "person_id" for the HR phase, "transaction_id" for the
// transactions phase).
type phaseTable struct {
	schema string
	table  string
	anchor string // column to MAX/DELETE on (NOT necessarily the table's own PK)
}

// resumeWatermark queries MAX(anchor) across all tables in the phase
// and returns the MIN — the largest anchor value for which every
// table is guaranteed to have committed its share. Tables empty so
// far contribute 0; if any table is empty, the watermark collapses
// to 0 and the phase loads from scratch.
func resumeWatermark(ctx context.Context, w Writer, phase string, tables []phaseTable) (int64, error) {
	var watermark int64 = -1
	for _, t := range tables {
		n, err := w.MaxBigint(ctx, t.schema, t.table, t.anchor)
		if err != nil {
			return 0, fmt.Errorf("%s phase: %w", phase, err)
		}
		if watermark < 0 || n < watermark {
			watermark = n
		}
	}
	if watermark < 0 {
		return 0, nil
	}
	return watermark, nil
}

// cleanupAbove removes rows whose anchor column exceeds threshold,
// in reverse table order so child rows are deleted before parents
// (FK-safe). Caller orders `tables` parent-first.
func cleanupAbove(ctx context.Context, w Writer, tables []phaseTable, threshold int64) error {
	for i := len(tables) - 1; i >= 0; i-- {
		t := tables[i]
		if err := w.DeleteAbove(ctx, t.schema, t.table, t.anchor, threshold); err != nil {
			return err
		}
	}
	return nil
}

// resume queries the watermark, runs cleanup, and logs the result.
// Returns the watermark (0 = fresh load, > 0 = resuming from this id).
// Pass `tables` parent-first; cleanup deletes children first.
func resume(ctx context.Context, w Writer, phase string, tables []phaseTable) (int64, error) {
	wm, err := resumeWatermark(ctx, w, phase, tables)
	if err != nil {
		return 0, err
	}
	if wm == 0 {
		return 0, nil
	}
	if err := cleanupAbove(ctx, w, tables, wm); err != nil {
		return 0, fmt.Errorf("%s cleanup above %d: %w", phase, wm, err)
	}
	fmt.Fprintf(os.Stderr, "  resume: %s phase will skip ids 1..%d (already loaded)\n", phase, wm)
	return wm, nil
}
