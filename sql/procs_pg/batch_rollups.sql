/* =============================================================
   batch — rollup refresh procedures (PostgreSQL port)
   -------------------------------------------------------------
   Small aggregate outputs -> single-statement TRUNCATE+INSERT, so
   these keep the EXCEPTION pattern (no COMMIT). BIT flags are real
   booleans now, so is_return=0 becomes NOT is_return, etc.
   ============================================================= */

CREATE OR REPLACE PROCEDURE batch.usp_refresh_agg_sales_by_month()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_agg_sales_by_month','dw.agg_sales_by_month','full');
    TRUNCATE TABLE dw.agg_sales_by_month;
    INSERT INTO dw.agg_sales_by_month(year_month,line_count,tx_count,units,gross_revenue_usd,return_count,
                                 return_value_usd,net_revenue_usd,software_revenue_usd,hardware_revenue_usd,avg_line_usd)
    SELECT to_char(occurred_at,'YYYY-MM'),
           COUNT(*), COUNT(DISTINCT transaction_id),
           SUM(CASE WHEN NOT is_return THEN quantity ELSE 0 END),
           SUM(CASE WHEN NOT is_return THEN line_total_usd ELSE 0 END),
           SUM(CASE WHEN is_return THEN 1 ELSE 0 END),
           SUM(CASE WHEN is_return THEN line_total_usd ELSE 0 END),
           SUM(line_total_usd),
           SUM(CASE WHEN NOT is_hardware THEN line_total_usd ELSE 0 END),
           SUM(CASE WHEN is_hardware THEN line_total_usd ELSE 0 END),
           CAST(AVG(line_total_usd) AS decimal(12,4))
    FROM dw.fact_sales GROUP BY to_char(occurred_at,'YYYY-MM');
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run; RAISE;
END $$;

CREATE OR REPLACE PROCEDURE batch.usp_refresh_agg_sales_by_month_platform()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_agg_sales_by_month_platform','dw.agg_sales_by_month_platform','full');
    TRUNCATE TABLE dw.agg_sales_by_month_platform;
    INSERT INTO dw.agg_sales_by_month_platform(year_month,platform_key,platform_name,units,line_count,revenue_usd)
    SELECT to_char(f.occurred_at,'YYYY-MM'), COALESCE(p.platform_key,-1), MAX(p.platform_name),
           SUM(CASE WHEN NOT f.is_return THEN f.quantity ELSE 0 END), COUNT(*), SUM(f.line_total_usd)
    FROM dw.fact_sales f LEFT JOIN dw.dim_product p ON p.product_key=f.product_key
    GROUP BY to_char(f.occurred_at,'YYYY-MM'), COALESCE(p.platform_key,-1);
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run; RAISE;
END $$;

CREATE OR REPLACE PROCEDURE batch.usp_refresh_agg_sales_by_day_shop()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_agg_sales_by_day_shop','dw.agg_sales_by_day_shop','full');
    TRUNCATE TABLE dw.agg_sales_by_day_shop;
    INSERT INTO dw.agg_sales_by_day_shop(date_key,shop_key,tx_count,line_count,units,revenue_usd)
    SELECT date_key, COALESCE(shop_key,-1), COUNT(DISTINCT transaction_id), COUNT(*),
           SUM(CASE WHEN NOT is_return THEN quantity ELSE 0 END), SUM(line_total_usd)
    FROM dw.fact_sales GROUP BY date_key, COALESCE(shop_key,-1);
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run; RAISE;
END $$;

CREATE OR REPLACE PROCEDURE batch.usp_refresh_agg_product_performance()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_agg_product_performance','dw.agg_product_performance','full');
    TRUNCATE TABLE dw.agg_product_performance;
    INSERT INTO dw.agg_product_performance(product_key,first_sold,last_sold,units_sold,line_count,revenue_usd,return_count,distinct_customers)
    SELECT product_key, MIN(occurred_at)::date, MAX(occurred_at)::date,
           SUM(CASE WHEN NOT is_return THEN quantity ELSE 0 END), COUNT(*), SUM(line_total_usd),
           SUM(CASE WHEN is_return THEN 1 ELSE 0 END), COUNT(DISTINCT customer_key)
    FROM dw.fact_sales GROUP BY product_key;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run; RAISE;
END $$;

CREATE OR REPLACE PROCEDURE batch.usp_refresh_agg_customer_ltv()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_agg_customer_ltv','dw.agg_customer_ltv','full');
    TRUNCATE TABLE dw.agg_customer_ltv;
    INSERT INTO dw.agg_customer_ltv(customer_key,first_purchase,last_purchase,order_count,line_count,units,gross_spend_usd,returns_usd,net_spend_usd)
    SELECT customer_key, MIN(occurred_at)::date, MAX(occurred_at)::date,
           COUNT(DISTINCT transaction_id), COUNT(*),
           SUM(CASE WHEN NOT is_return THEN quantity ELSE 0 END),
           SUM(CASE WHEN NOT is_return THEN line_total_usd ELSE 0 END),
           SUM(CASE WHEN is_return THEN line_total_usd ELSE 0 END),
           SUM(line_total_usd)
    FROM dw.fact_sales WHERE customer_key IS NOT NULL GROUP BY customer_key;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run; RAISE;
END $$;

CREATE OR REPLACE PROCEDURE batch.usp_refresh_agg_web_traffic_daily()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_agg_web_traffic_daily','dw.agg_web_traffic_daily','full');
    TRUNCATE TABLE dw.agg_web_traffic_daily;
    INSERT INTO dw.agg_web_traffic_daily(date_key,page_views,sessions,bot_views,human_views,logged_in_views,distinct_countries)
    SELECT date_key, COUNT(*), COUNT(DISTINCT session_id),
           SUM(CASE WHEN is_bot THEN 1 ELSE 0 END), SUM(CASE WHEN is_bot THEN 0 ELSE 1 END),
           SUM(CASE WHEN account_id IS NOT NULL THEN 1 ELSE 0 END),
           COUNT(DISTINCT client_country)
    FROM dw.fact_web_activity GROUP BY date_key;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run; RAISE;
END $$;

CREATE OR REPLACE PROCEDURE batch.usp_refresh_agg_review_sentiment_by_month()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_agg_review_sentiment_by_month','dw.agg_review_sentiment_by_month','full');
    TRUNCATE TABLE dw.agg_review_sentiment_by_month;
    INSERT INTO dw.agg_review_sentiment_by_month(year_month,review_count,avg_rating,rating_only_count,verified_count)
    SELECT to_char(posted_at,'YYYY-MM'), COUNT(*),
           CAST(AVG(rating::float8) AS decimal(4,3)),
           SUM(CASE WHEN body='' THEN 1 ELSE 0 END),
           SUM(CASE WHEN is_verified_purchase THEN 1 ELSE 0 END)
    FROM web.reviews GROUP BY to_char(posted_at,'YYYY-MM');
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run; RAISE;
END $$;

/* ---- orchestrators ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_all_rollups()
LANGUAGE plpgsql AS $$
BEGIN
    CALL batch.usp_refresh_agg_sales_by_month();
    CALL batch.usp_refresh_agg_sales_by_month_platform();
    CALL batch.usp_refresh_agg_sales_by_day_shop();
    CALL batch.usp_refresh_agg_product_performance();
    CALL batch.usp_refresh_agg_customer_ltv();
    CALL batch.usp_refresh_agg_web_traffic_daily();
    CALL batch.usp_refresh_agg_review_sentiment_by_month();
END $$;

/* ---- the overnight batch: dims -> facts -> rollups -> wide OBT, end to end.
   No columnstore steps (Postgres has none); the fact/wide procs manage their
   own BRIN + covering btrees. NO exception handler here — it CALLs the
   COMMIT-driven fact/wide procedures. ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_everything(p_full boolean DEFAULT true)
LANGUAGE plpgsql AS $$
BEGIN
    CALL batch.usp_refresh_all_dimensions();
    CALL batch.usp_refresh_all_facts(p_full);
    CALL batch.usp_refresh_all_rollups();
    CALL batch.usp_refresh_sales_wide(p_full);
END $$;
