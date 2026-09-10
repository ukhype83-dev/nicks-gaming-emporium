/* =============================================================
   batch — sales_wide OBT load (PostgreSQL port of usp_refresh_sales_wide)
   -------------------------------------------------------------
   Denormalise fact_sales + dims into the wide one-big-table, month
   by month. SQL Server stored this as a clustered columnstore; here
   it is a plain heap with a BRIN on occurred_at built after the load
   (see 40_wide.sql / the port plan). COMMIT per month -> no EXCEPTION
   handler. The columnstore build/drop/rebuild maintenance procs have
   no Postgres analogue and are intentionally omitted.
   ============================================================= */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_sales_wide(p_full boolean DEFAULT true, p_start date DEFAULT NULL, p_end date DEFAULT NULL)
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint := 0; v_m date; v_mend date; v_start date := p_start; v_end date := p_end; v_n bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_sales_wide','dw.sales_wide', CASE WHEN p_full THEN 'full' ELSE 'incremental' END);
    IF v_start IS NULL THEN SELECT MIN(occurred_at)::date INTO v_start FROM dw.fact_sales; END IF;
    IF v_end   IS NULL THEN SELECT MAX(occurred_at)::date INTO v_end   FROM dw.fact_sales; END IF;
    IF p_full THEN
        TRUNCATE TABLE dw.sales_wide;
        DROP INDEX IF EXISTS dw.brin_sales_wide_occurred;
    END IF;
    v_m := date_trunc('month', v_start)::date;
    WHILE v_m <= v_end LOOP
        v_mend := (v_m + interval '1 month')::date;
        IF NOT p_full THEN DELETE FROM dw.sales_wide WHERE occurred_at >= v_m AND occurred_at < v_mend; END IF;
        INSERT INTO dw.sales_wide(transaction_line_id,transaction_id,occurred_at,date_key,year,month,
            product_key,product_title,product_kind,platform_name,genre,publisher,
            customer_key,customer_country,customer_region,loyalty_tier,
            shop_key,shop_name,shop_country,shop_region,channel_name,condition_name,currency_code,
            quantity,line_total,line_total_usd,is_return,is_hardware)
        SELECT f.transaction_line_id,f.transaction_id,f.occurred_at,f.date_key,
               extract(year from f.occurred_at)::smallint, extract(month from f.occurred_at)::smallint,
               f.product_key,p.title,p.product_kind,p.platform_name,p.category,p.publisher,
               f.customer_key,c.country_code,c.region,c.loyalty_tier,
               f.shop_key,s.name,s.country_code,s.region,ch.channel_name,co.condition_name,f.currency_code,
               f.quantity,f.line_total,f.line_total_usd,f.is_return,f.is_hardware
        FROM dw.fact_sales f
        LEFT JOIN dw.dim_product   p  ON p.product_key   = f.product_key
        LEFT JOIN dw.dim_customer  c  ON c.customer_key  = f.customer_key
        LEFT JOIN dw.dim_shop      s  ON s.shop_key      = f.shop_key
        LEFT JOIN dw.dim_channel   ch ON ch.channel_key  = f.channel_key
        LEFT JOIN dw.dim_condition co ON co.condition_key= f.condition_key
        WHERE f.occurred_at >= v_m AND f.occurred_at < v_mend;
        GET DIAGNOSTICS v_n = ROW_COUNT;
        v_rows := v_rows + v_n;
        COMMIT;
        v_m := v_mend;
    END LOOP;
    IF p_full THEN
        CREATE INDEX brin_sales_wide_occurred ON dw.sales_wide USING brin(occurred_at);
    END IF;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
END $$;
