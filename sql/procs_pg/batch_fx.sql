/* =============================================================
   batch — FX dimension + USD correction (PostgreSQL port)
   ============================================================= */

/* ---- build the dense as-of FX dimension (currency x year) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_dim_fx(p_start_year smallint DEFAULT 1986, p_end_year smallint DEFAULT 2016)
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_dim_fx','dw.dim_fx','full');
    TRUNCATE TABLE dw.dim_fx;
    WITH yrs AS (SELECT generate_series(p_start_year::int, p_end_year::int) AS yr),
         curr AS (SELECT DISTINCT currency_code FROM dbo.fx_rates)
    INSERT INTO dw.dim_fx(currency_code, year, rate_to_usd)
    SELECT c.currency_code, y.yr::smallint,
           COALESCE(
             (SELECT f.rate_to_usd FROM dbo.fx_rates f
                WHERE f.currency_code=c.currency_code AND f.effective_year<=y.yr
                ORDER BY f.effective_year DESC LIMIT 1),
             (SELECT f.rate_to_usd FROM dbo.fx_rates f
                WHERE f.currency_code=c.currency_code
                ORDER BY f.effective_year ASC LIMIT 1))
    FROM curr c CROSS JOIN yrs y;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run;
    RAISE;
END $$;

/* ---- correct line_total_usd / offer_amount_usd in place, month by month,
   from dim_fx. Cheaper than a full fact rebuild (no OLTP re-read). Month loop
   COMMITs per month, so this procedure carries NO exception handler. A
   correlated subquery preserves the T-SQL LEFT JOIN semantics (rows without an
   FX match keep divisor 1.0). ---- */
CREATE OR REPLACE PROCEDURE batch.usp_fix_fact_usd()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint := 0; v_m date; v_mend date; v_end date; v_n bigint;
BEGIN
    v_run := batch.log_start('batch.usp_fix_fact_usd','dw.fact_sales/tradein','full');
    SELECT date_trunc('month', MIN(occurred_at))::date, MAX(occurred_at)::date
      INTO v_m, v_end FROM dw.fact_sales;
    WHILE v_m <= v_end LOOP
        v_mend := (v_m + interval '1 month')::date;
        UPDATE dw.fact_sales f
        SET line_total_usd = CAST(f.line_total / COALESCE(NULLIF(
              (SELECT x.rate_to_usd FROM dw.dim_fx x
                 WHERE x.currency_code=f.currency_code AND x.year=extract(year from f.occurred_at)::smallint),0),1.0) AS decimal(14,4))
        WHERE f.occurred_at >= v_m AND f.occurred_at < v_mend;
        GET DIAGNOSTICS v_n = ROW_COUNT;
        v_rows := v_rows + v_n;
        COMMIT;
        v_m := v_mend;
    END LOOP;
    -- fact_tradein (smaller; single pass is fine)
    UPDATE dw.fact_tradein t
    SET offer_amount_usd = CAST(t.offer_amount / COALESCE(NULLIF(
          (SELECT x.rate_to_usd FROM dw.dim_fx x
             WHERE x.currency_code=t.currency_code AND x.year=extract(year from t.occurred_at)::smallint),0),1.0) AS decimal(14,4));
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
END $$;

/* ---- one-shot USD correction for an already-built DW: dim_fx -> fix fact USD
   -> re-agg rollups -> rebuild OBT. (No columnstore steps: Postgres has none.) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_fix_all_usd()
LANGUAGE plpgsql AS $$
BEGIN
    CALL batch.usp_refresh_dim_fx();
    CALL batch.usp_fix_fact_usd();
    CALL batch.usp_refresh_all_rollups();
    CALL batch.usp_refresh_sales_wide(true);
END $$;
