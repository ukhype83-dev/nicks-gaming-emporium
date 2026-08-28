/* =============================================================
   batch — fact load procedures (PostgreSQL port, month-by-month)
   -------------------------------------------------------------
   Each month is its own INSERT + COMMIT so the load is never one
   giant transaction (the web-load lesson). Because these procedures
   COMMIT, they carry NO EXCEPTION handler (COMMIT and EXCEPTION are
   mutually exclusive in one PL/pgSQL block) — an error propagates
   and fails the build loudly.

   Index strategy (replaces SQL Server's clustered index + DISABLE/
   REBUILD of nonclustered indexes): for a @full load, drop the BRIN
   + covering btrees, load month-by-month, then rebuild them once at
   the end — the same "keep random index writes out of the bulk load"
   idea. BRIN on occurred_at fits the month-ordered load perfectly.
   READ-ONLY on OLTP; writes only dw.
   ============================================================= */

/* ---- fact_sales (grain = transaction_lines with a product) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_fact_sales(p_full boolean DEFAULT true, p_start date DEFAULT NULL, p_end date DEFAULT NULL)
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint := 0; v_m date; v_mend date; v_start date := p_start; v_end date := p_end; v_n bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_fact_sales','dw.fact_sales', CASE WHEN p_full THEN 'full' ELSE 'incremental' END);
    IF v_start IS NULL THEN SELECT MIN(occurred_at)::date INTO v_start FROM public.transactions; END IF;
    IF v_end   IS NULL THEN SELECT MAX(occurred_at)::date INTO v_end   FROM public.transactions; END IF;
    IF p_full THEN
        TRUNCATE TABLE dw.fact_sales;
        DROP INDEX IF EXISTS dw.brin_fact_sales_occurred;
        DROP INDEX IF EXISTS dw.ix_fact_sales_product;
        DROP INDEX IF EXISTS dw.ix_fact_sales_customer;
        DROP INDEX IF EXISTS dw.ix_fact_sales_shop;
        DROP INDEX IF EXISTS dw.ix_fact_sales_date;
    END IF;

    v_m := date_trunc('month', v_start)::date;
    WHILE v_m <= v_end LOOP
        v_mend := (v_m + interval '1 month')::date;
        IF NOT p_full THEN DELETE FROM dw.fact_sales WHERE occurred_at >= v_m AND occurred_at < v_mend; END IF;
        INSERT INTO dw.fact_sales(transaction_line_id,transaction_id,occurred_at,date_key,product_key,customer_key,
                             shop_key,staff_key,channel_key,condition_key,currency_code,quantity,unit_price,
                             line_discount,line_tax,line_total,line_total_usd,is_return,is_hardware)
        SELECT tl.transaction_line_id, t.transaction_id, t.occurred_at,
               to_char(t.occurred_at,'YYYYMMDD')::int,
               CASE WHEN tl.release_id IS NOT NULL THEN tl.release_id ELSE 9000000000 + tl.hardware_id END,
               t.customer_id, t.shop_id, t.staff_id, ch.channel_key, co.condition_key, t.currency_code,
               tl.quantity, tl.unit_price, tl.line_discount, tl.line_tax, tl.line_total,
               CAST(tl.line_total / COALESCE(NULLIF(fx.rate_to_usd,0),1.0) AS decimal(14,4)),
               (t.original_transaction_id IS NOT NULL),
               (tl.hardware_id IS NOT NULL)
        FROM public.transactions t
        JOIN public.transaction_lines tl ON tl.transaction_id = t.transaction_id
        LEFT JOIN dw.dim_channel   ch ON ch.channel_name   = t.channel
        LEFT JOIN dw.dim_condition co ON co.condition_name = tl.condition
        LEFT JOIN dw.dim_fx        fx ON fx.currency_code  = t.currency_code AND fx.year = extract(year from t.occurred_at)::smallint
        WHERE t.occurred_at >= v_m AND t.occurred_at < v_mend
          AND (tl.release_id IS NOT NULL OR tl.hardware_id IS NOT NULL);
        GET DIAGNOSTICS v_n = ROW_COUNT;
        v_rows := v_rows + v_n;
        COMMIT;
        v_m := v_mend;
    END LOOP;

    IF p_full THEN
        CREATE INDEX brin_fact_sales_occurred ON dw.fact_sales USING brin(occurred_at);
        CREATE INDEX ix_fact_sales_product  ON dw.fact_sales(product_key);
        CREATE INDEX ix_fact_sales_customer ON dw.fact_sales(customer_key);
        CREATE INDEX ix_fact_sales_shop     ON dw.fact_sales(shop_key);
        CREATE INDEX ix_fact_sales_date     ON dw.fact_sales(date_key);
    END IF;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
END $$;

/* ---- fact_tradein (grain = trade_in_items) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_fact_tradein(p_full boolean DEFAULT true, p_start date DEFAULT NULL, p_end date DEFAULT NULL)
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint := 0; v_m date; v_mend date; v_start date := p_start; v_end date := p_end; v_n bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_fact_tradein','dw.fact_tradein', CASE WHEN p_full THEN 'full' ELSE 'incremental' END);
    IF v_start IS NULL THEN SELECT MIN(occurred_at)::date INTO v_start FROM public.trade_ins; END IF;
    IF v_end   IS NULL THEN SELECT MAX(occurred_at)::date INTO v_end   FROM public.trade_ins; END IF;
    IF p_full THEN
        TRUNCATE TABLE dw.fact_tradein;
        DROP INDEX IF EXISTS dw.brin_fact_tradein_occurred;
        DROP INDEX IF EXISTS dw.ix_fact_tradein_product;
    END IF;
    v_m := date_trunc('month', v_start)::date;
    WHILE v_m <= v_end LOOP
        v_mend := (v_m + interval '1 month')::date;
        IF NOT p_full THEN DELETE FROM dw.fact_tradein WHERE occurred_at >= v_m AND occurred_at < v_mend; END IF;
        INSERT INTO dw.fact_tradein(trade_in_item_id,trade_in_id,occurred_at,date_key,product_key,customer_key,
                               shop_key,condition_key,currency_code,offer_amount,offer_amount_usd,is_hardware)
        SELECT ti.trade_in_item_id, ti.trade_in_id, tr.occurred_at,
               to_char(tr.occurred_at,'YYYYMMDD')::int,
               CASE WHEN ti.release_id IS NOT NULL THEN ti.release_id
                    WHEN ti.hardware_id IS NOT NULL THEN 9000000000 + ti.hardware_id ELSE NULL END,
               tr.customer_id, tr.shop_id, co.condition_key, tr.currency_code,
               ti.valuation,
               CAST(ti.valuation / COALESCE(NULLIF(fx.rate_to_usd,0),1.0) AS decimal(14,4)),
               (ti.hardware_id IS NOT NULL)
        FROM public.trade_ins tr
        JOIN public.trade_in_items ti ON ti.trade_in_id = tr.trade_in_id
        LEFT JOIN dw.dim_condition co ON co.condition_name = ti.condition
        LEFT JOIN dw.dim_fx fx ON fx.currency_code = tr.currency_code AND fx.year = extract(year from tr.occurred_at)::smallint
        WHERE tr.occurred_at >= v_m AND tr.occurred_at < v_mend;
        GET DIAGNOSTICS v_n = ROW_COUNT;
        v_rows := v_rows + v_n;
        COMMIT;
        v_m := v_mend;
    END LOOP;
    IF p_full THEN
        CREATE INDEX brin_fact_tradein_occurred ON dw.fact_tradein USING brin(occurred_at);
        CREATE INDEX ix_fact_tradein_product ON dw.fact_tradein(product_key);
    END IF;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
END $$;

/* ---- fact_web_activity (grain = web.page_views) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_fact_web_activity(p_full boolean DEFAULT true, p_start date DEFAULT NULL, p_end date DEFAULT NULL)
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint := 0; v_m date; v_mend date; v_start date := p_start; v_end date := p_end; v_n bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_fact_web_activity','dw.fact_web_activity', CASE WHEN p_full THEN 'full' ELSE 'incremental' END);
    IF v_start IS NULL THEN SELECT MIN(occurred_at)::date INTO v_start FROM web.page_views; END IF;
    IF v_end   IS NULL THEN SELECT MAX(occurred_at)::date INTO v_end   FROM web.page_views; END IF;
    IF p_full THEN
        TRUNCATE TABLE dw.fact_web_activity;
        DROP INDEX IF EXISTS dw.brin_fact_web_occurred;
    END IF;
    v_m := date_trunc('month', v_start)::date;
    WHILE v_m <= v_end LOOP
        v_mend := (v_m + interval '1 month')::date;
        IF NOT p_full THEN DELETE FROM dw.fact_web_activity WHERE occurred_at >= v_m AND occurred_at < v_mend; END IF;
        INSERT INTO dw.fact_web_activity(page_view_id,session_id,occurred_at,date_key,account_id,customer_key,
                                    url_path,product_key,client_country,user_agent_family,is_bot,http_status,bytes_sent)
        SELECT pv.page_view_id, pv.session_id, pv.occurred_at,
               to_char(pv.occurred_at,'YYYYMMDD')::int,
               pv.account_id, a.customer_id, pv.url_path,
               CASE WHEN pv.url_path ~ '^/reviews/[0-9]'
                     AND substring(pv.url_path from 10 for 20) ~ '^[0-9]+$'
                    THEN substring(pv.url_path from 10 for 20)::bigint END,
               pv.client_country, pv.user_agent_family,
               (pv.user_agent_family IN ('Googlebot','bingbot','YahooSlurp','crawler')),
               pv.http_status, pv.bytes_sent
        FROM web.page_views pv
        LEFT JOIN web.accounts a ON a.account_id = pv.account_id
        WHERE pv.occurred_at >= v_m AND pv.occurred_at < v_mend;
        GET DIAGNOSTICS v_n = ROW_COUNT;
        v_rows := v_rows + v_n;
        COMMIT;
        v_m := v_mend;
    END LOOP;
    IF p_full THEN
        CREATE INDEX brin_fact_web_occurred ON dw.fact_web_activity USING brin(occurred_at);
    END IF;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
END $$;

/* ---- orchestrator ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_all_facts(p_full boolean DEFAULT true)
LANGUAGE plpgsql AS $$
BEGIN
    CALL batch.usp_refresh_fact_sales(p_full);
    CALL batch.usp_refresh_fact_tradein(p_full);
    CALL batch.usp_refresh_fact_web_activity(p_full);
END $$;
