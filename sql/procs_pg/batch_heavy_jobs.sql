/* =============================================================
   batch — heavy / long-running overnight jobs (PostgreSQL port)
   -------------------------------------------------------------
   In-character back-office jobs that run long (RBAR loops, CPU burn,
   idle waits, a lock-holding close). Side-effect PROCEDUREs (they
   stamp batch.job_control); their status echo becomes a RAISE NOTICE.
   WAITFOR DELAY -> pg_sleep; the CPU busy-loop uses clock_timestamp()
   (now() is transaction-fixed in Postgres and would never advance).
   ============================================================= */

/* control table the jobs stamp / lock */
CREATE TABLE IF NOT EXISTS batch.job_control (
    job_name    varchar(128) NOT NULL PRIMARY KEY,
    status      varchar(16)  NOT NULL DEFAULT 'idle',
    last_run_at timestamp(3) NULL,
    run_count   bigint       NOT NULL DEFAULT 0
);
INSERT INTO batch.job_control(job_name) VALUES
    ('supplier_price_feed'),('supplier_stock_poll'),('loyalty_recompute'),
    ('customer_tier_recompute'),('month_end_close'),('dashboard_refresh'),
    ('statement_run'),('eod_reconciliation')
ON CONFLICT (job_name) DO NOTHING;

/* ---- pulls the distributor price feed over the legacy gateway (slow) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_ImportSupplierPriceFeed(p_timeout_seconds int DEFAULT 30)
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_sleep(p_timeout_seconds);                       -- waiting on the remote pull
    UPDATE batch.job_control SET status='ok', last_run_at=now() AT TIME ZONE 'UTC', run_count=run_count+1
        WHERE job_name='supplier_price_feed';
    RAISE NOTICE 'supplier price feed: % seconds', p_timeout_seconds;
END $$;

/* ---- polls the supplier stock service; latency varies ---- */
CREATE OR REPLACE PROCEDURE batch.usp_PollSupplierStock(p_max_wait_seconds int DEFAULT 60)
LANGUAGE plpgsql AS $$
DECLARE v_s int := 1 + floor(random() * p_max_wait_seconds)::int;
BEGIN
    PERFORM pg_sleep(v_s);
    UPDATE batch.job_control SET status='ok', last_run_at=now() AT TIME ZONE 'UTC', run_count=run_count+1
        WHERE job_name='supplier_stock_poll';
    RAISE NOTICE 'supplier stock poll: waited % seconds', v_s;
END $$;

/* ---- recompute loyalty point balances: a CPU burn loop for @seconds ---- */
CREATE OR REPLACE PROCEDURE batch.usp_RecalculateLoyaltyPoints(p_seconds int DEFAULT 30)
LANGUAGE plpgsql AS $$
DECLARE v_end timestamptz := clock_timestamp() + (p_seconds || ' seconds')::interval;
        v_acc double precision := 1.0; v_i bigint := 0;
BEGIN
    WHILE clock_timestamp() < v_end LOOP
        v_acc := sqrt(abs(v_acc * 1.61803399 + v_i)) + 1.0;   -- the "points curve"
        v_i := v_i + 1;
    END LOOP;
    UPDATE batch.job_control SET status='ok', last_run_at=now() AT TIME ZONE 'UTC', run_count=run_count+1
        WHERE job_name='loyalty_recompute';
    RAISE NOTICE 'loyalty recompute: % iterations', v_i;
END $$;

/* ---- recompute each customer's tier one at a time (RBAR, scan per member) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_RecalculateCustomerTiers(p_customers int DEFAULT 5000)
LANGUAGE plpgsql AS $$
DECLARE v_ck bigint; v_spend decimal(18,2); v_done int := 0;
BEGIN
    FOR v_ck IN SELECT customer_key FROM dw.agg_customer_ltv ORDER BY net_spend_usd DESC LIMIT p_customers LOOP
        SELECT SUM(line_total_usd) INTO v_spend FROM dw.fact_sales WHERE customer_key = v_ck;  -- scan per member
        v_done := v_done + 1;
    END LOOP;
    UPDATE batch.job_control SET status='ok', last_run_at=now() AT TIME ZONE 'UTC', run_count=run_count+1
        WHERE job_name='customer_tier_recompute';
    RAISE NOTICE 'customer tiers: % members retiered', v_done;
END $$;

/* ---- builds the reporting date spine with a recursive CTE ---- */
CREATE OR REPLACE PROCEDURE batch.usp_GenerateDateSpine(p_days int DEFAULT 500000)
LANGUAGE plpgsql AS $$
DECLARE v_days bigint; v_last date;
BEGIN
    WITH RECURSIVE spine AS (
        SELECT 1 AS n, DATE '1986-01-01' AS dt
        UNION ALL SELECT n+1, dt + 1 FROM spine WHERE n < p_days
    )
    SELECT COUNT(*), MAX(dt) INTO v_days, v_last FROM spine;   -- no MAXRECURSION cap in PG
    RAISE NOTICE 'date spine: % days generated, last %', v_days, v_last;
END $$;

/* ---- month-end close: holds the close lock across the posting window ---- */
CREATE OR REPLACE PROCEDURE batch.usp_MonthEndClose(p_hold_seconds int DEFAULT 30)
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE batch.job_control SET status='closing', last_run_at=now() AT TIME ZONE 'UTC', run_count=run_count+1
        WHERE job_name='month_end_close';                 -- row lock held for the close
    PERFORM pg_sleep(p_hold_seconds);                     -- posting the period (lock still held)
    UPDATE batch.job_control SET status='closed' WHERE job_name='month_end_close';
    RAISE NOTICE 'month-end close: held % seconds', p_hold_seconds;
END $$;

/* ---- overnight: precompute the morning dashboard numbers ---- */
CREATE OR REPLACE PROCEDURE batch.usp_NightlyDashboardRefresh()
LANGUAGE plpgsql AS $$
DECLARE v_a bigint; v_b bigint; v_c bigint; v_e bigint;
BEGIN
    SELECT COUNT(*) INTO v_a FROM dw.fact_sales WHERE is_hardware;
    SELECT COUNT(*) INTO v_b FROM dw.fact_sales WHERE is_return;
    SELECT COUNT(*) INTO v_c FROM dw.fact_web_activity WHERE is_bot;
    SELECT COUNT(*) INTO v_e FROM dw.sales_wide WHERE line_total_usd > 50;
    UPDATE batch.job_control SET status='ok', last_run_at=now() AT TIME ZONE 'UTC', run_count=run_count+1
        WHERE job_name='dashboard_refresh';
    RAISE NOTICE 'dashboard: hw=% returns=% bots=% big=%', v_a, v_b, v_c, v_e;
END $$;

/* ---- monthly statement run: one per second (mail-relay throttle) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_EmailMonthlyStatements(p_count int DEFAULT 30)
LANGUAGE plpgsql AS $$
DECLARE v_i int := 0;
BEGIN
    WHILE v_i < p_count LOOP
        PERFORM pg_sleep(1);                              -- mail-relay throttle
        v_i := v_i + 1;
    END LOOP;
    UPDATE batch.job_control SET status='ok', last_run_at=now() AT TIME ZONE 'UTC', run_count=run_count+1
        WHERE job_name='statement_run';
    RAISE NOTICE 'statements sent: %', v_i;
END $$;

/* ---- end-of-day reconciliation: recompute + audit pass + settle ---- */
CREATE OR REPLACE PROCEDURE batch.usp_EndOfDayReconciliation(p_seconds int DEFAULT 45)
LANGUAGE plpgsql AS $$
DECLARE v_third int := p_seconds / 3;
BEGIN
    CALL batch.usp_RecalculateLoyaltyPoints(v_third);
    PERFORM batch.drain(rpt.usp_rpt_AnnualSalesAudit(2));   -- run the heavy audit report
    PERFORM pg_sleep(v_third);                              -- settlement window
    UPDATE batch.job_control SET status='ok', last_run_at=now() AT TIME ZONE 'UTC', run_count=run_count+1
        WHERE job_name='eod_reconciliation';
END $$;
