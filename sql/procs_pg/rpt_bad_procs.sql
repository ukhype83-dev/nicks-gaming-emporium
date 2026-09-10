/* =============================================================
   rpt — the "as-built" BAD procs (PostgreSQL port)
   -------------------------------------------------------------
   Deliberately awful, in-character reports — the tuning-lab material.
   The port PRESERVES the badness: scalar UDFs are VOLATILE (never
   inlined), non-SARGable predicates stay non-SARGable, cursors stay
   RBAR. DO NOT "fix" these — the badness is the point. Where an
   anti-pattern behaves differently on Postgres (e.g. usp_TillReport's
   implicit conversion would ERROR instead of scanning), a minimal
   change keeps it runnable-but-slow rather than making it correct.
   ============================================================= */

/* ---- the UDF tax: a scalar function called once per row. VOLATILE so
   Postgres never inlines/caches it — one lookup per row, like T-SQL. ---- */
CREATE OR REPLACE FUNCTION public.fn_GetPlatformName(p_platform_id int)
RETURNS varchar(100) LANGUAGE plpgsql VOLATILE AS $$
DECLARE n varchar(100);
BEGIN
    SELECT name INTO n FROM public.platforms WHERE platform_id = p_platform_id;
    RETURN COALESCE(n, 'Unknown Platform');
END $$;

/* ---- Todd's famous sales report: SELECT *, UDF per row, YEAR() on the
   column (non-SARGable), UDF in ORDER BY too. ---- */
CREATE OR REPLACE FUNCTION rpt.usp_ToddsSalesReport(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT *,
           public.fn_GetPlatformName(p.platform_key) AS PlatformNameAgain,   -- UDF tax
           f.line_total * 1.0 AS RevenueMaybe
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE extract(year from f.occurred_at) = COALESCE(p_year, extract(year from f.occurred_at))  -- kills the index
    ORDER BY public.fn_GetPlatformName(p.platform_key), f.line_total DESC;    -- UDF in ORDER BY too
    RETURN c;
END $$;

/* ---- "Nick just wants the number." Whole-table scan, function on the
   date column vs a string literal. ---- */
CREATE OR REPLACE FUNCTION rpt.usp_MonthlyNumberForBoss(p_month_str varchar(7) DEFAULT '2010-12')
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT SUM(line_total_usd) AS TheNumber
    FROM dw.fact_sales
    WHERE to_char(occurred_at,'YYYY-MM') = p_month_str;   -- non-SARGable
    RETURN c;
END $$;

/* ---- game search: leading-wildcard LIKE with UPPER() on the column. ---- */
CREATE OR REPLACE FUNCTION rpt.usp_FindGamesLikeThis(p_term varchar(100))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT product_key, title, platform_name, category, publisher
    FROM dw.dim_product
    WHERE UPPER(title) LIKE '%' || UPPER(p_term) || '%'      -- function on column + leading %
       OR UPPER(publisher) LIKE '%' || UPPER(p_term) || '%'
    ORDER BY title;
    RETURN c;
END $$;

/* ---- one proc, every optional filter OR'd together (parameter-sniffing). ---- */
CREATE OR REPLACE FUNCTION rpt.usp_CustomerSearch(p_country char(2) DEFAULT NULL, p_loyalty varchar(16) DEFAULT NULL,
    p_city varchar(200) DEFAULT NULL, p_signup_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT customer_key, status, signup_date, country_code, city, loyalty_tier, age_band
    FROM dw.dim_customer
    WHERE (p_country IS NULL OR country_code = p_country)
      AND (p_loyalty IS NULL OR loyalty_tier = p_loyalty)
      AND (p_city    IS NULL OR city = p_city)
      AND (p_signup_year IS NULL OR signup_year = p_signup_year)
    ORDER BY signup_date DESC;
    RETURN c;
END $$;

/* ---- views on views on views. Each layer adds "just one more join". ---- */
CREATE OR REPLACE VIEW rpt.v_sales_base AS
    SELECT f.transaction_line_id, f.occurred_at, f.product_key, f.customer_key,
           f.shop_key, f.line_total_usd, f.is_return
    FROM dw.fact_sales f;
CREATE OR REPLACE VIEW rpt.v_sales_enriched AS
    SELECT b.*, p.title, p.platform_name, p.category, p.publisher
    FROM rpt.v_sales_base b
    LEFT JOIN dw.dim_product p ON p.product_key = b.product_key;
CREATE OR REPLACE VIEW rpt.v_sales_final AS
    SELECT e.*, c.country_code, c.loyalty_tier, s.name AS shop_name, s.region
    FROM rpt.v_sales_enriched e
    LEFT JOIN dw.dim_customer c ON c.customer_key = e.customer_key
    LEFT JOIN dw.dim_shop s ON s.shop_key = e.shop_key;

CREATE OR REPLACE FUNCTION rpt.usp_SalesReportV2_FINAL_v3(p_publisher varchar(300) DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT *
    FROM rpt.v_sales_final
    WHERE (p_publisher IS NULL OR publisher = p_publisher)
    ORDER BY occurred_at DESC
    LIMIT 1000;
    RETURN c;
END $$;

/* ---- the till report: till_id is text ('7A','T1','3'); everyone passes an
   int. On SQL Server that's an implicit conversion + scan; on Postgres a raw
   text=int comparison ERRORS, so cast the parameter to text to keep it
   runnable-but-scanning (the badness — no useful index on till_id — remains). */
CREATE OR REPLACE FUNCTION rpt.usp_TillReport(p_till int DEFAULT 1)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT t.till_id, COUNT(*) AS txns, SUM(t.total) AS total_local
    FROM public.transactions t
    WHERE t.till_id = p_till::text                    -- text column vs int param (kept runnable)
    GROUP BY t.till_id;
    RETURN c;
END $$;

/* ---- "who bought what": DISTINCT to hide a fan-out + a correlated
   subquery that runs once per returned row (N+1). ---- */
CREATE OR REPLACE FUNCTION rpt.usp_WhoBoughtWhat(p_product_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT DISTINCT c.customer_key, c.country_code,
           (SELECT COUNT(*) FROM dw.fact_sales f2
            WHERE f2.customer_key = c.customer_key) AS total_lines_ever   -- N+1 per row
    FROM dw.fact_sales f
    JOIN dw.dim_customer c ON c.customer_key = f.customer_key
    WHERE f.product_key = p_product_key;
    RETURN c;
END $$;

/* ---- top customers, one row at a time in a cursor (RBAR). ---- */
CREATE OR REPLACE FUNCTION rpt.usp_GetTopCustomersSLOW(p_top int DEFAULT 20)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; v_ck bigint;
BEGIN
    DROP TABLE IF EXISTS _gettopcust;
    CREATE TEMP TABLE _gettopcust (customer_key bigint, spend decimal(18,2)) ON COMMIT DROP;
    FOR v_ck IN SELECT DISTINCT customer_key FROM dw.fact_sales WHERE customer_key IS NOT NULL LOOP
        INSERT INTO _gettopcust SELECT v_ck, SUM(line_total_usd) FROM dw.fact_sales WHERE customer_key = v_ck;  -- one scan per customer
    END LOOP;
    OPEN c FOR SELECT * FROM _gettopcust ORDER BY spend DESC LIMIT p_top;
    RETURN c;
END $$;
