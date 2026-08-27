/* =============================================================
   rpt — the BAD procs, volume 2 (PostgreSQL port). Badness preserved.
   ============================================================= */

/* ---- two more scalar UDFs (VOLATILE) to spread the UDF tax around ---- */
CREATE OR REPLACE FUNCTION dbo.fn_GetShopName(p_shop_id bigint)
RETURNS varchar(255) LANGUAGE plpgsql VOLATILE AS $$
DECLARE n varchar(255);
BEGIN
    SELECT name INTO n FROM dbo.shops WHERE shop_id = p_shop_id;   -- one lookup per row
    RETURN COALESCE(n, '(unknown shop)');
END $$;

CREATE OR REPLACE FUNCTION dbo.fn_GetCustomerCountry(p_customer_id bigint)
RETURNS char(2) LANGUAGE plpgsql VOLATILE AS $$
BEGIN
    RETURN (SELECT country_code FROM dbo.customer_addresses WHERE customer_id = p_customer_id LIMIT 1);
END $$;

/* ---- inventory "check" that cursors over every shop, one query each ---- */
CREATE OR REPLACE FUNCTION rpt.usp_ToddsInventoryCheck()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; v_sid bigint;
BEGIN
    DROP TABLE IF EXISTS _toddsinv;
    CREATE TEMP TABLE _toddsinv (shop_id bigint, skus int, units bigint) ON COMMIT DROP;
    FOR v_sid IN SELECT shop_id FROM dbo.shops LOOP
        INSERT INTO _toddsinv SELECT v_sid, COUNT(*), SUM(on_hand) FROM dbo.inventory WHERE shop_id = v_sid;  -- one scan per shop
    END LOOP;
    OPEN c FOR SELECT r.*, dbo.fn_GetShopName(r.shop_id) AS shop_name FROM _toddsinv r ORDER BY units DESC;  -- + UDF tax
    RETURN c;
END $$;

/* ---- "dedupe" customers with functions on both sides of a self-join. ---- */
CREATE OR REPLACE FUNCTION rpt.usp_FindDuplicateCustomers(p_country char(2) DEFAULT 'IE')
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT a.customer_key AS cust_a, b.customer_key AS cust_b, a.city
    FROM dw.dim_customer a
    JOIN dw.dim_customer b
      ON UPPER(TRIM(a.city)) = UPPER(TRIM(b.city))   -- functions kill any index
     AND a.birth_year = b.birth_year
     AND a.customer_key < b.customer_key
    WHERE a.country_code = p_country AND b.country_code = p_country
    ORDER BY a.city;
    RETURN c;
END $$;

/* ---- yearly totals built one year at a time in a cursor ---- */
CREATE OR REPLACE FUNCTION rpt.usp_SalesByYear_Cursor()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; v_yr int := 1986;
BEGIN
    DROP TABLE IF EXISTS _salesbyyr;
    CREATE TEMP TABLE _salesbyyr (yr int, rev decimal(18,2)) ON COMMIT DROP;
    WHILE v_yr <= 2016 LOOP
        INSERT INTO _salesbyyr SELECT v_yr, SUM(line_total_usd) FROM dw.fact_sales WHERE extract(year from occurred_at) = v_yr;  -- scan per year
        v_yr := v_yr + 1;
    END LOOP;
    OPEN c FOR SELECT * FROM _salesbyyr ORDER BY yr;
    RETURN c;
END $$;

/* ---- the naming crime + views-on-views (reuses rpt.v_sales_final) ---- */
CREATE OR REPLACE FUNCTION rpt.usp_CustomerReport_vFinal_USE_THIS_ONE(p_customer_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT * FROM rpt.v_sales_final WHERE customer_key = p_customer_key ORDER BY occurred_at DESC;
    RETURN c;
END $$;

/* ---- big spenders via a scalar UDF in the WHERE + non-SARGable ---- */
CREATE OR REPLACE FUNCTION rpt.usp_GetBigSpenders_Slow(p_country char(2) DEFAULT 'US')
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT c.customer_key, l.net_spend_usd, dbo.fn_GetCustomerCountry(c.customer_key) AS country_again
    FROM dw.dim_customer c
    JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    WHERE dbo.fn_GetCustomerCountry(c.customer_key) = p_country      -- UDF per row instead of c.country_code
      AND l.net_spend_usd > 1000
    ORDER BY l.net_spend_usd DESC;
    RETURN c;
END $$;

/* ---- "what sold last Christmas" — functions on the date column + OR soup ---- */
CREATE OR REPLACE FUNCTION rpt.usp_WhatSoldLastChristmas(p_year int DEFAULT 2007)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.title, p.platform_name, SUM(f.quantity) AS units
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE extract(year from f.occurred_at) = p_year                              -- non-SARGable
      AND (extract(month from f.occurred_at) = 12 OR extract(month from f.occurred_at) = 11)  -- OR soup, functions
    GROUP BY p.title, p.platform_name
    ORDER BY units DESC;
    RETURN c;
END $$;

/* ---- top reviewers computed with an N+1 correlated subquery per row ---- */
CREATE OR REPLACE FUNCTION rpt.usp_TopReviewersNPlusOne(p_min_reviews int DEFAULT 50)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT a.account_id, a.username,
           (SELECT COUNT(*) FROM web.reviews r WHERE r.account_id = a.account_id) AS review_count,
           (SELECT AVG(r.rating::float8) FROM web.reviews r WHERE r.account_id = a.account_id) AS avg_rating
    FROM web.accounts a
    WHERE (SELECT COUNT(*) FROM web.reviews r WHERE r.account_id = a.account_id) >= p_min_reviews  -- subquery for EVERY account
    ORDER BY review_count DESC;
    RETURN c;
END $$;
