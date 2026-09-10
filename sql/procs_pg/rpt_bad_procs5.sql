/* =============================================================
   rpt — the BAD procs, volume 5 (PostgreSQL port). Badness preserved.
   ============================================================= */

/* ---- nested cursors: RBAR squared. genuinely slow. ---- */
CREATE OR REPLACE FUNCTION rpt.usp_MonthlyBreakdown_CursorInCursor(p_year int DEFAULT 2009)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; v_mo int; v_sk bigint;
BEGIN
    DROP TABLE IF EXISTS _mbcic;
    CREATE TEMP TABLE _mbcic (yr int, mo int, shop_key bigint, rev decimal(18,2)) ON COMMIT DROP;
    FOR v_mo IN SELECT DISTINCT extract(month from occurred_at)::int FROM dw.fact_sales WHERE extract(year from occurred_at)=p_year LOOP
        FOR v_sk IN SELECT shop_key FROM dw.dim_shop WHERE is_flagship OR shop_key<=200 LIMIT 20 LOOP
            INSERT INTO _mbcic SELECT p_year, v_mo, v_sk, SUM(line_total_usd) FROM dw.fact_sales
                WHERE extract(year from occurred_at)=p_year AND extract(month from occurred_at)=v_mo AND shop_key=v_sk;  -- scan per (month,shop)
        END LOOP;
    END LOOP;
    OPEN c FOR SELECT * FROM _mbcic ORDER BY mo, rev DESC;
    RETURN c;
END $$;

/* ---- non-SARGable review search over the big text column ---- */
CREATE OR REPLACE FUNCTION rpt.usp_ReviewSearch_NonSargable(p_term varchar(100))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT review_id, account_id, rating, LEFT(body,120) AS snippet
    FROM web.reviews WHERE body LIKE '%' || p_term || '%'   -- leading wildcard on 12M+ text rows
    ORDER BY helpful_count DESC
    LIMIT 500;
    RETURN c;
END $$;

/* ---- "the numbers" but one full scan per table, sequentially ---- */
CREATE OR REPLACE FUNCTION rpt.usp_CountEverything_ManyScans()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT 'customers' AS entity, COUNT(*) AS n FROM public.customers WHERE status='active'
    UNION ALL SELECT 'accounts', COUNT(*) FROM web.accounts WHERE status IS NOT NULL
    UNION ALL SELECT 'reviews', COUNT(*) FROM web.reviews WHERE rating >= 1
    UNION ALL SELECT 'fact_sales', COUNT(*) FROM dw.fact_sales WHERE line_total >= 0
    UNION ALL SELECT 'transactions', COUNT(*) FROM public.transactions WHERE total >= 0;
    RETURN c;
END $$;

/* ---- customer rank via a correlated subquery per row (O(n^2)-ish) ---- */
CREATE OR REPLACE FUNCTION rpt.usp_GetCustomerRank(p_customer_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p_customer_key AS customer_key,
           (SELECT net_spend_usd FROM dw.agg_customer_ltv WHERE customer_key=p_customer_key) AS spend,
           (SELECT COUNT(*)+1 FROM dw.agg_customer_ltv x
            WHERE x.net_spend_usd > (SELECT net_spend_usd FROM dw.agg_customer_ltv WHERE customer_key=p_customer_key)) AS rank_position;
    RETURN c;
END $$;

/* ---- SELECT * everything a customer ever did, unbounded ---- */
CREATE OR REPLACE FUNCTION rpt.usp_EverySingleSale(p_customer_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR SELECT * FROM dw.sales_wide WHERE customer_key = p_customer_key ORDER BY occurred_at;
    RETURN c;
END $$;

/* ---- product lookup that LIKEs every text column with wildcards ---- */
CREATE OR REPLACE FUNCTION rpt.usp_ProductLookup_LikeEverything(p_term varchar(100))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT * FROM dw.dim_product
    WHERE title LIKE '%'||p_term||'%' OR publisher LIKE '%'||p_term||'%'
       OR developer LIKE '%'||p_term||'%' OR category LIKE '%'||p_term||'%'
       OR platform_name LIKE '%'||p_term||'%' OR media_type LIKE '%'||p_term||'%';
    RETURN c;
END $$;

/* ---- deliberately heavy: scans sales_wide with an un-indexable predicate ---- */
CREATE OR REPLACE FUNCTION rpt.usp_ReportThatTakesForever()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.platform_name, COUNT(*) AS lines, SUM(sw.line_total_usd) AS revenue_usd
    FROM dw.sales_wide sw
    JOIN dw.dim_product p ON p.product_key = sw.product_key
    WHERE sw.product_title LIKE '%' || LOWER(sw.platform_name) || '%'   -- correlated LIKE, per-row, whole scan
    GROUP BY p.platform_name ORDER BY revenue_usd DESC;
    RETURN c;
END $$;

/* ---- which shops are open: functions on the date columns ---- */
CREATE OR REPLACE FUNCTION rpt.usp_WhichShopsWereOpen(p_on_date date)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT shop_key, name, country_code
    FROM dw.dim_shop
    WHERE extract(year from opened_date) <= extract(year from p_on_date)
      AND (closed_date IS NULL OR to_char(closed_date,'YYYY-MM-DD') >= to_char(p_on_date,'YYYY-MM-DD'))
    ORDER BY name;
    RETURN c;
END $$;

/* ---- temp-table shuffle: same data copied through three temp tables ---- */
CREATE OR REPLACE FUNCTION rpt.usp_TopCustomers_TempShuffle(p_top int DEFAULT 50)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    DROP TABLE IF EXISTS _tcs_a; DROP TABLE IF EXISTS _tcs_b; DROP TABLE IF EXISTS _tcs_c;
    CREATE TEMP TABLE _tcs_a ON COMMIT DROP AS SELECT customer_key, net_spend_usd FROM dw.agg_customer_ltv;
    CREATE TEMP TABLE _tcs_b ON COMMIT DROP AS SELECT * FROM _tcs_a WHERE net_spend_usd > 0;
    CREATE TEMP TABLE _tcs_c ON COMMIT DROP AS SELECT * FROM _tcs_b;   -- why not
    OPEN c FOR
    SELECT cc.customer_key, cc.net_spend_usd, dc.country_code
    FROM _tcs_c cc JOIN dw.dim_customer dc ON dc.customer_key = cc.customer_key
    ORDER BY cc.net_spend_usd DESC LIMIT p_top;
    RETURN c;
END $$;

/* ---- the quarterly report Barry insists on: yet another view layer ---- */
CREATE OR REPLACE FUNCTION rpt.usp_QuarterlyReport_LegacyView(p_year int DEFAULT 2010)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT extract(year from occurred_at) AS yr, extract(quarter from occurred_at) AS q,
           SUM(line_total_usd) AS revenue_usd
    FROM rpt.v_sales_final
    WHERE extract(year from occurred_at) = p_year
    GROUP BY extract(year from occurred_at), extract(quarter from occurred_at)
    ORDER BY q;
    RETURN c;
END $$;
