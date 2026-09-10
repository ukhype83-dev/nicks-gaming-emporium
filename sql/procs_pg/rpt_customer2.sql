/* =============================================================
   rpt — customer analytics, part 2 (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_NewVsReturning()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    -- a customer's first purchase year = "new" that year; later years = "returning"
    SELECT extract(year from f.occurred_at) AS yr,
           SUM(CASE WHEN extract(year from f.occurred_at)=extract(year from l.first_purchase) THEN 1 ELSE 0 END) AS new_buyer_lines,
           SUM(CASE WHEN extract(year from f.occurred_at) >extract(year from l.first_purchase) THEN 1 ELSE 0 END) AS returning_lines
    FROM dw.fact_sales f
    JOIN dw.agg_customer_ltv l ON l.customer_key = f.customer_key
    WHERE NOT f.is_return
    GROUP BY extract(year from f.occurred_at) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_SpendDeciles()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    WITH d AS (SELECT net_spend_usd, NTILE(10) OVER (ORDER BY net_spend_usd) AS decile FROM dw.agg_customer_ltv)
    SELECT decile, COUNT(*) AS customers,
           CAST(MIN(net_spend_usd) AS decimal(12,2)) AS min_spend,
           CAST(MAX(net_spend_usd) AS decimal(12,2)) AS max_spend,
           CAST(SUM(net_spend_usd) AS decimal(16,2)) AS total_spend
    FROM d GROUP BY decile ORDER BY decile;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_CustomerGeoHeatmap(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT c.region, c.country_code,
           COUNT(DISTINCT f.customer_key) AS buyers,
           SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_customer c ON c.customer_key = f.customer_key
    WHERE (p_year IS NULL OR extract(year from f.occurred_at) = p_year)
    GROUP BY c.region, c.country_code ORDER BY revenue_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_OneAndDoneCustomers()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    -- share of customers who bought exactly once (churn signal)
    SELECT COUNT(*) AS total_buyers,
           SUM(CASE WHEN order_count=1 THEN 1 ELSE 0 END) AS one_and_done,
           CAST(100.0*SUM(CASE WHEN order_count=1 THEN 1 ELSE 0 END)/NULLIF(COUNT(*),0) AS decimal(5,1)) AS one_and_done_pct
    FROM dw.agg_customer_ltv;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_LoyaltyVsSpend()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT COALESCE(c.loyalty_tier, '(none)') AS tier,
           COUNT(*) AS buyers,
           CAST(AVG(l.order_count*1.0) AS decimal(8,2)) AS avg_orders,
           CAST(AVG(l.net_spend_usd) AS decimal(12,2)) AS avg_spend_usd
    FROM dw.agg_customer_ltv l
    JOIN dw.dim_customer c ON c.customer_key = l.customer_key
    GROUP BY c.loyalty_tier ORDER BY avg_spend_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_CohortByYear()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT c.signup_year,
           COUNT(*) AS cohort_size,
           SUM(CASE WHEN l.customer_key IS NOT NULL THEN 1 ELSE 0 END) AS ever_bought,
           CAST(AVG(COALESCE(l.net_spend_usd,0)) AS decimal(12,2)) AS avg_ltv_usd
    FROM dw.dim_customer c
    LEFT JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    GROUP BY c.signup_year ORDER BY c.signup_year;
    RETURN c;
END $$;
