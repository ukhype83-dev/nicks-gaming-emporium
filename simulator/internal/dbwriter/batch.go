// BatchedLoader — accumulates rows in memory and flushes them via
// Writer.BulkInsert when the buffer reaches batchSize.
//
// Sticky-error semantics: once a flush fails, the loader stores the
// error and all further Add/Flush calls short-circuit to returning
// it. Callers should check Err() after generation but before the
// final Flush to short-circuit out cleanly.
package dbwriter

import (
	"context"
	"fmt"
	"os"
	"time"
)

const defaultBatchSize = 50_000

type BatchedLoader struct {
	writer    Writer
	ctx       context.Context
	schema    string
	table     string
	columns   []string
	batchSize int
	buffer    [][]any
	total     int64
	err       error // sticky — once set, future calls short-circuit
}

// NewBatchedLoader wires a BatchedLoader onto a Writer for a specific
// target table. batchSize <= 0 uses defaultBatchSize.
func NewBatchedLoader(ctx context.Context, w Writer, schema, table string, columns []string, batchSize int) *BatchedLoader {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	return &BatchedLoader{
		writer: w, ctx: ctx,
		schema: schema, table: table,
		columns:   columns,
		batchSize: batchSize,
		buffer:    make([][]any, 0, batchSize),
	}
}

// Add buffers a row; auto-flushes when batchSize is reached. If any
// prior Flush failed, Add is a no-op and returns the stored error.
func (b *BatchedLoader) Add(row []any) error {
	if b.err != nil {
		return b.err
	}
	b.buffer = append(b.buffer, row)
	if len(b.buffer) >= b.batchSize {
		return b.Flush()
	}
	return nil
}

// Flush writes the buffered rows to the DB and clears the buffer.
// Always call at the end of a load. Safe to call multiple times;
// no-op if buffer is empty.
//
// On failure: stores the error (sticky), clears the buffer (so the
// caller can't accidentally retry the same rows), prints the error
// to stderr so it's visible, and returns the error.
func (b *BatchedLoader) Flush() error {
	if b.err != nil {
		return b.err
	}
	if len(b.buffer) == 0 {
		return nil
	}
	n := len(b.buffer)
	fmt.Fprintf(os.Stderr, "  → flushing %d rows into %s.%s (running total %d)...\n",
		n, b.schema, b.table, b.total+int64(n))
	start := time.Now()
	if err := b.writer.BulkInsert(b.ctx, b.schema, b.table, b.columns, b.buffer); err != nil {
		b.err = err
		b.buffer = b.buffer[:0]
		fmt.Fprintf(os.Stderr, "  ✗ BulkInsert into %s.%s FAILED after %s: %v\n",
			b.schema, b.table, time.Since(start).Round(time.Millisecond), err)
		return err
	}
	b.total += int64(n)
	fmt.Fprintf(os.Stderr, "    committed %d rows into %s.%s in %s (total %d)\n",
		n, b.schema, b.table, time.Since(start).Round(time.Millisecond), b.total)
	b.buffer = b.buffer[:0]
	return nil
}

// Err returns the sticky error if a prior Flush failed, else nil.
func (b *BatchedLoader) Err() error { return b.err }

// Total returns the number of rows successfully flushed so far.
func (b *BatchedLoader) Total() int64 { return b.total }

// SeedTotal pre-loads the running-total counter. Used by resumable
// loads so progress messages and the final stats reflect the cumulative
// DB row count, not just rows added in this run.
func (b *BatchedLoader) SeedTotal(n int64) { b.total = n }
