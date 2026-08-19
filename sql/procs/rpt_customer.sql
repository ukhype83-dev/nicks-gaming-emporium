CREATE OR ALTER PROCEDURE rpt.usp_rpt_TopSpenders
    @top INT = 25, @country CHAR(2) = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) l.customer_key, c.country_code, c.city, c.loyalty_tier,
           l.order_count, l.units, l.net_spend_usd, l.first_purchase, l.last_purchase
    FROM dw.agg_customer_ltv l
    JOIN dw.dim_customer c ON c.customer_key = l.customer_key
    WHERE (@country IS NULL OR c.country_code = @country)
    ORDER BY l.net_spend_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_CustomerLTV
    @customer_key BIGINT
AS
BEGIN
    SET NOCOUNT ON;
    SELECT c.customer_key, c.status, c.country_code, c.city, c.loyalty_tier, c.signup_cohort,
           l.order_count, l.line_count, l.units, l.gross_spend_usd, l.returns_usd, l.net_spend_usd,
           l.first_purchase, l.last_purchase
    FROM dw.dim_customer c
    LEFT JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    WHERE c.customer_key = @customer_key;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_CustomersByCountry
AS
BEGIN
    SET NOCOUNT ON;
    SELECT c.country_code, g.region, COUNT_BIG(*) AS customers,
           SUM(CASE WHEN l.customer_key IS NOT NULL THEN 1 ELSE 0 END) AS buyers,
           SUM(ISNULL(l.net_spend_usd,0)) AS net_spend_usd
    FROM dw.dim_customer c
    LEFT JOIN dw.dim_geography g ON g.country_code = c.country_code
    LEFT JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    GROUP BY c.country_code, g.region
    ORDER BY net_spend_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_LoyaltyTierValue
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ISNULL(c.loyalty_tier, N'(none)') AS loyalty_tier,
           COUNT_BIG(*) AS customers,
           CAST(AVG(ISNULL(l.net_spend_usd,0)) AS DECIMAL(14,2)) AS avg_net_spend_usd,
           SUM(ISNULL(l.net_spend_usd,0)) AS total_net_spend_usd
    FROM dw.dim_customer c
    LEFT JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    GROUP BY c.loyalty_tier
    ORDER BY total_net_spend_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_CohortRetention
    @cohort_year SMALLINT = 2008
AS
BEGIN
    SET NOCOUNT ON;
    -- how many of a signup-year cohort were still buying N years later
    SELECT c.signup_cohort,
           COUNT_BIG(*) AS cohort_size,
           SUM(CASE WHEN l.last_purchase >= DATEFROMPARTS(@cohort_year+1,1,1) THEN 1 ELSE 0 END) AS active_y1,
           SUM(CASE WHEN l.last_purchase >= DATEFROMPARTS(@cohort_year+3,1,1) THEN 1 ELSE 0 END) AS active_y3,
           SUM(CASE WHEN l.last_purchase >= DATEFROMPARTS(@cohort_year+5,1,1) THEN 1 ELSE 0 END) AS active_y5
    FROM dw.dim_customer c
    LEFT JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    WHERE c.signup_year = @cohort_year
    GROUP BY c.signup_cohort
    ORDER BY c.signup_cohort;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_AgeBandSpend
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ISNULL(c.age_band,N'(unknown)') AS age_band,
           COUNT_BIG(*) AS customers,
           SUM(ISNULL(l.net_spend_usd,0)) AS net_spend_usd,
           CAST(AVG(ISNULL(l.net_spend_usd,0)) AS DECIMAL(14,2)) AS avg_spend_usd
    FROM dw.dim_customer c
    LEFT JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    GROUP BY c.age_band ORDER BY net_spend_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_RFMSegments
    @as_of DATE = '2016-09-30'
AS
BEGIN
    SET NOCOUNT ON;
    ;WITH rfm AS (
        SELECT customer_key,
               DATEDIFF(DAY, last_purchase, @as_of) AS recency_days,
               order_count AS frequency,
               net_spend_usd AS monetary,
               NTILE(5) OVER (ORDER BY last_purchase)   AS r_score,
               NTILE(5) OVER (ORDER BY order_count)     AS f_score,
               NTILE(5) OVER (ORDER BY net_spend_usd)   AS m_score
        FROM dw.agg_customer_ltv
    )
    SELECT CONCAT(r_score,f_score,m_score) AS rfm_cell,
           COUNT_BIG(*) AS customers, SUM(monetary) AS net_spend_usd
    FROM rfm GROUP BY CONCAT(r_score,f_score,m_score)
    ORDER BY net_spend_usd DESC;
END
GO
