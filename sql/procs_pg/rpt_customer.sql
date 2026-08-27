/* =============================================================
   rpt — customer reports (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TopSpenders(p_top int DEFAULT 25, p_country char(2) DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT l.customer_key, c.country_code, c.city, c.loyalty_tier,
           l.order_count, l.units, l.net_spend_usd, l.first_purchase, l.last_purchase
    FROM dw.agg_customer_ltv l
    JOIN dw.dim_customer c ON c.customer_key = l.customer_key
    WHERE (p_country IS NULL OR c.country_code = p_country)
    ORDER BY l.net_spend_usd DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_CustomerLTV(p_customer_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT c.customer_key, c.status, c.country_code, c.city, c.loyalty_tier, c.signup_cohort,
           l.order_count, l.line_count, l.units, l.gross_spend_usd, l.returns_usd, l.net_spend_usd,
           l.first_purchase, l.last_purchase
    FROM dw.dim_customer c
    LEFT JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    WHERE c.customer_key = p_customer_key;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_CustomersByCountry()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT c.country_code, g.region, COUNT(*) AS customers,
           SUM(CASE WHEN l.customer_key IS NOT NULL THEN 1 ELSE 0 END) AS buyers,
           SUM(COALESCE(l.net_spend_usd,0)) AS net_spend_usd
    FROM dw.dim_customer c
    LEFT JOIN dw.dim_geography g ON g.country_code = c.country_code
    LEFT JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    GROUP BY c.country_code, g.region
    ORDER BY net_spend_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_LoyaltyTierValue()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT COALESCE(c.loyalty_tier, '(none)') AS loyalty_tier,
           COUNT(*) AS customers,
           CAST(AVG(COALESCE(l.net_spend_usd,0)) AS decimal(14,2)) AS avg_net_spend_usd,
           SUM(COALESCE(l.net_spend_usd,0)) AS total_net_spend_usd
    FROM dw.dim_customer c
    LEFT JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    GROUP BY c.loyalty_tier
    ORDER BY total_net_spend_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_CohortRetention(p_cohort_year int DEFAULT 2008)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    -- how many of a signup-year cohort were still buying N years later
    SELECT c.signup_cohort,
           COUNT(*) AS cohort_size,
           SUM(CASE WHEN l.last_purchase >= make_date(p_cohort_year+1,1,1) THEN 1 ELSE 0 END) AS active_y1,
           SUM(CASE WHEN l.last_purchase >= make_date(p_cohort_year+3,1,1) THEN 1 ELSE 0 END) AS active_y3,
           SUM(CASE WHEN l.last_purchase >= make_date(p_cohort_year+5,1,1) THEN 1 ELSE 0 END) AS active_y5
    FROM dw.dim_customer c
    LEFT JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    WHERE c.signup_year = p_cohort_year
    GROUP BY c.signup_cohort
    ORDER BY c.signup_cohort;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_AgeBandSpend()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT COALESCE(c.age_band,'(unknown)') AS age_band,
           COUNT(*) AS customers,
           SUM(COALESCE(l.net_spend_usd,0)) AS net_spend_usd,
           CAST(AVG(COALESCE(l.net_spend_usd,0)) AS decimal(14,2)) AS avg_spend_usd
    FROM dw.dim_customer c
    LEFT JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    GROUP BY c.age_band ORDER BY net_spend_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_RFMSegments(p_as_of date DEFAULT '2016-09-30')
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    WITH rfm AS (
        SELECT customer_key,
               (p_as_of - last_purchase) AS recency_days,
               order_count AS frequency,
               net_spend_usd AS monetary,
               NTILE(5) OVER (ORDER BY last_purchase)   AS r_score,
               NTILE(5) OVER (ORDER BY order_count)     AS f_score,
               NTILE(5) OVER (ORDER BY net_spend_usd)   AS m_score
        FROM dw.agg_customer_ltv
    )
    SELECT CONCAT(r_score,f_score,m_score) AS rfm_cell,
           COUNT(*) AS customers, SUM(monetary) AS net_spend_usd
    FROM rfm GROUP BY CONCAT(r_score,f_score,m_score)
    ORDER BY net_spend_usd DESC;
    RETURN c;
END $$;
