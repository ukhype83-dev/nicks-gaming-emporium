/* =============================================================
   rpt — the BAD procs, volume 3 (PostgreSQL port). Badness preserved.
   ============================================================= */

/* ---- everything about a customer: five result sets of SELECT * ---- */
CREATE OR REPLACE FUNCTION rpt.usp_GetEverythingAboutACustomer(p_customer_id bigint)
RETURNS SETOF refcursor LANGUAGE plpgsql AS $$
DECLARE c1 refcursor; c2 refcursor; c3 refcursor; c4 refcursor; c5 refcursor;
BEGIN
    OPEN c1 FOR SELECT * FROM public.customers WHERE customer_id = p_customer_id; RETURN NEXT c1;
    OPEN c2 FOR SELECT * FROM public.customer_addresses WHERE customer_id = p_customer_id; RETURN NEXT c2;
    OPEN c3 FOR SELECT * FROM public.loyalty_memberships WHERE customer_id = p_customer_id; RETURN NEXT c3;
    OPEN c4 FOR SELECT * FROM dw.agg_customer_ltv WHERE customer_key = p_customer_id; RETURN NEXT c4;
    OPEN c5 FOR SELECT * FROM dw.fact_sales WHERE customer_key = p_customer_id ORDER BY occurred_at; RETURN NEXT c5;
END $$;

/* ---- fuzzy product search: LOWER() + leading-wildcard on three columns ---- */
CREATE OR REPLACE FUNCTION rpt.usp_SearchProductsFuzzy(p_term varchar(100))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT product_key, title, platform_name, publisher, developer
    FROM dw.dim_product
    WHERE LOWER(title)     LIKE '%' || LOWER(p_term) || '%'
       OR LOWER(publisher) LIKE '%' || LOWER(p_term) || '%'
       OR LOWER(developer) LIKE '%' || LOWER(p_term) || '%'
    ORDER BY title;
    RETURN c;
END $$;

/* ---- running monthly total via a cursor (a window function would do) ---- */
CREATE OR REPLACE FUNCTION rpt.usp_RunningTotalCursor(p_year int DEFAULT 2008)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; r record; v_run decimal(18,2) := 0;
BEGIN
    DROP TABLE IF EXISTS _runtot;
    CREATE TEMP TABLE _runtot (ym char(7), rev decimal(18,2), running decimal(18,2)) ON COMMIT DROP;
    FOR r IN SELECT year_month, net_revenue_usd FROM dw.agg_sales_by_month
             WHERE LEFT(year_month,4)=p_year::text ORDER BY year_month LOOP
        v_run := v_run + r.net_revenue_usd;
        INSERT INTO _runtot VALUES (r.year_month, r.net_revenue_usd, v_run);
    END LOOP;
    OPEN c FOR SELECT * FROM _runtot ORDER BY ym;
    RETURN c;
END $$;

/* ---- separate scans in a UNION instead of one GROUP BY ---- */
CREATE OR REPLACE FUNCTION rpt.usp_YearlyComparison_ManyScans()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT 2006 AS yr, SUM(line_total_usd) AS rev FROM dw.fact_sales WHERE extract(year from occurred_at)=2006
    UNION ALL SELECT 2007, SUM(line_total_usd) FROM dw.fact_sales WHERE extract(year from occurred_at)=2007
    UNION ALL SELECT 2008, SUM(line_total_usd) FROM dw.fact_sales WHERE extract(year from occurred_at)=2008
    UNION ALL SELECT 2009, SUM(line_total_usd) FROM dw.fact_sales WHERE extract(year from occurred_at)=2009
    UNION ALL SELECT 2010, SUM(line_total_usd) FROM dw.fact_sales WHERE extract(year from occurred_at)=2010;
    RETURN c;
END $$;

/* ---- customer order history, but a CAST on the key defeats the index ---- */
CREATE OR REPLACE FUNCTION rpt.usp_CustomerOrderHistory_Slow(p_customer_id bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT f.occurred_at, p.title, f.quantity, f.line_total_usd
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE CAST(f.customer_key AS varchar(20)) = CAST(p_customer_id AS varchar(20))  -- CAST kills the seek
    ORDER BY f.occurred_at;
    RETURN c;
END $$;

/* ---- "the numbers" dashboard: a pile of COUNT(DISTINCT) scans in one row ---- */
CREATE OR REPLACE FUNCTION rpt.usp_AllTheNumbers()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT
        (SELECT COUNT(DISTINCT customer_key) FROM dw.fact_sales) AS distinct_buyers,
        (SELECT COUNT(DISTINCT product_key)  FROM dw.fact_sales) AS distinct_products,
        (SELECT COUNT(DISTINCT shop_key)     FROM dw.fact_sales) AS distinct_shops,
        (SELECT COUNT(DISTINCT staff_key)    FROM dw.fact_sales) AS distinct_staff,
        (SELECT COUNT(*) FROM dw.fact_sales WHERE is_return)     AS total_returns;
    RETURN c;
END $$;

/* ---- the "do not delete" report, layered on the view stack ---- */
CREATE OR REPLACE FUNCTION rpt.usp_ShopReport_vOLD_dont_delete(p_region varchar(32) DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT shop_name, region, SUM(line_total_usd) AS rev
    FROM rpt.v_sales_final
    WHERE (p_region IS NULL OR region = p_region)
    GROUP BY shop_name, region
    ORDER BY rev DESC
    LIMIT 5000;
    RETURN c;
END $$;
