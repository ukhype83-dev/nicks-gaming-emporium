/* =============================================================
   rpt — the BAD procs, volume 4 (PostgreSQL port). Badness preserved.
   ============================================================= */

/* ---- dumps the entire wide OBT, no filter ---- */
CREATE OR REPLACE FUNCTION rpt.usp_ReportEverything()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR SELECT * FROM dw.sales_wide ORDER BY occurred_at DESC LIMIT 50000;
    RETURN c;
END $$;

/* ---- the NOT IN / NULL trap: release_id is nullable, so this silently
   returns nothing the moment any review has a NULL release_id. Postgres has
   the same three-valued-logic semantics, so the trap ports EXACTLY. ---- */
CREATE OR REPLACE FUNCTION rpt.usp_FindProductsNeverReviewed()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT product_key, title, platform_name
    FROM dw.dim_product
    WHERE product_kind = 'game'
      AND product_key NOT IN (SELECT release_id FROM web.reviews);   -- NULLs -> empty result
    RETURN c;
END $$;

/* ---- top sellers, platform via the UDF tax instead of the join column ---- */
CREATE OR REPLACE FUNCTION rpt.usp_TopSellersWithUDF(p_top int DEFAULT 50)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.title, dbo.fn_GetPlatformName(p.platform_key) AS platform,
           pp.units_sold, pp.revenue_usd
    FROM dw.agg_product_performance pp
    JOIN dw.dim_product p ON p.product_key = pp.product_key
    ORDER BY pp.units_sold DESC
    LIMIT p_top;
    RETURN c;
END $$;

/* ---- buyers per year, counted one year at a time in a WHILE loop ---- */
CREATE OR REPLACE FUNCTION rpt.usp_CountBuyersByYear_Cursor()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; v_y int := 1996;
BEGIN
    DROP TABLE IF EXISTS _cntbuyers;
    CREATE TEMP TABLE _cntbuyers (yr int, buyers int) ON COMMIT DROP;
    WHILE v_y <= 2016 LOOP
        INSERT INTO _cntbuyers SELECT v_y, COUNT(DISTINCT customer_key) FROM dw.fact_sales WHERE extract(year from occurred_at)=v_y;
        v_y := v_y + 1;
    END LOOP;
    OPEN c FOR SELECT * FROM _cntbuyers ORDER BY yr;
    RETURN c;
END $$;

/* ---- search "by anything": OR across many columns, functions everywhere ---- */
CREATE OR REPLACE FUNCTION rpt.usp_SearchByAnything(p_term varchar(100))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT product_key, title, platform_name, publisher, developer, category
    FROM dw.dim_product
    WHERE LOWER(title)         LIKE '%'||LOWER(p_term)||'%'
       OR LOWER(publisher)     LIKE '%'||LOWER(p_term)||'%'
       OR LOWER(developer)     LIKE '%'||LOWER(p_term)||'%'
       OR LOWER(category)      LIKE '%'||LOWER(p_term)||'%'
       OR LOWER(platform_name) LIKE '%'||LOWER(p_term)||'%'
       OR CAST(product_key AS varchar(20)) = p_term;
    RETURN c;
END $$;

/* ---- builds a monthly sales "email" by string-concatenating in a loop ---- */
CREATE OR REPLACE FUNCTION rpt.usp_MonthlySalesEmail_StringBuilder(p_year int DEFAULT 2008)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; v_body text := 'Sales report for ' || p_year::text || chr(10); r record;
BEGIN
    FOR r IN SELECT year_month, net_revenue_usd FROM dw.agg_sales_by_month
             WHERE LEFT(year_month,4)=p_year::text ORDER BY year_month LOOP
        v_body := v_body || r.year_month || ': $' || (r.net_revenue_usd::bigint)::text || chr(10);
    END LOOP;
    OPEN c FOR SELECT v_body AS email_body;
    RETURN c;
END $$;
