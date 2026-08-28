package dbwriter

import (
	"context"
	"fmt"
)

// populateCustomerStatus reclassifies customers to 'dormant' when their most
// recent transaction predates the ~6-month window before the 2016-09-30
// closure (cutoff 2016-03-30), or when they never transacted. Without this
// the whole estate ships 'active' (customers.go seeds every row 'active'),
// which is implausible for a retailer that had been shedding customers for
// years — ~95% were dormant at closure.
//
// Runs post-transaction (recency is only known once dbo.transactions is
// loaded) and computes directly from dbo.transactions, so it has no
// dependency on the reporting/DW layer. Recent buyers are materialised once
// into a temp table; the UPDATE is batched by customer_id range so each
// statement autocommits and SIMPLE recovery truncates the log between
// batches. Idempotent — re-running only flips stale 'active' rows.
func populateCustomerStatus(ctx context.Context, w Writer, s *LoadAllStats, progress func(string, int64)) error {
	// Nothing to classify against if transactions weren't loaded this run —
	// leave the seeded 'active' status untouched rather than mark everyone
	// dormant off an empty transaction table.
	txns, err := w.MaxBigint(ctx, "dbo", "transactions", "transaction_id")
	if err != nil {
		return err
	}
	if txns == 0 {
		return nil
	}

	stmtMSSQL := `
SET NOCOUNT ON;
IF OBJECT_ID('tempdb..#recent_buyers') IS NOT NULL DROP TABLE #recent_buyers;
SELECT DISTINCT customer_id
INTO #recent_buyers
FROM dbo.transactions
WHERE customer_id IS NOT NULL
  AND occurred_at >= '2016-03-30';
CREATE UNIQUE CLUSTERED INDEX ix_recent ON #recent_buyers(customer_id);

DECLARE @lo BIGINT = 1,
        @step BIGINT = 1000000,
        @max BIGINT = (SELECT MAX(customer_id) FROM dbo.customers);
WHILE @lo <= @max
BEGIN
    UPDATE c SET c.status = N'dormant'
    FROM dbo.customers c
    WHERE c.customer_id >= @lo AND c.customer_id < @lo + @step
      AND c.status = N'active'
      AND NOT EXISTS (SELECT 1 FROM #recent_buyers r WHERE r.customer_id = c.customer_id);
    SET @lo += @step;
END
DROP TABLE #recent_buyers;
`

	// PostgreSQL: same result, simpler shape. No batched WHILE loop (that
	// was SQL Server SIMPLE-recovery log management) — one UPDATE over a
	// materialised recent-buyers anti-join. TEMP table lives for the one
	// implicit transaction pgx runs the multi-statement block in.
	stmtPostgres := `
CREATE TEMP TABLE recent_buyers AS
    SELECT DISTINCT customer_id FROM public.transactions
    WHERE customer_id IS NOT NULL AND occurred_at >= DATE '2016-03-30';
CREATE UNIQUE INDEX ON recent_buyers(customer_id);
UPDATE public.customers c
    SET status = 'dormant'
    WHERE c.status = 'active'
      AND NOT EXISTS (SELECT 1 FROM recent_buyers r WHERE r.customer_id = c.customer_id);
DROP TABLE recent_buyers;
`

	stmt := stmtMSSQL
	if _, ok := w.(*Postgres); ok {
		stmt = stmtPostgres
	}
	if err := w.ExecSQL(ctx, stmt); err != nil {
		return fmt.Errorf("customer status reclassification: %w", err)
	}
	progress("dbo.customers (status)", 1)
	return nil
}
