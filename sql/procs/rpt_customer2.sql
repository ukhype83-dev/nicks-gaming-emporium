CREATE OR ALTER PROCEDURE rpt.usp_rpt_NewVsReturning
AS
BEGIN
    SET NOCOUNT ON;
    -- a customer's first purchase year = "new" that year; later years = "returning"
    SELECT YEAR(f.occurred_at) AS yr,
           SUM(CASE WHEN YEAR(f.occurred_at)=YEAR(l.first_purchase) THEN 1 ELSE 0 END) AS new_buyer_lines,
           SUM(CASE WHEN YEAR(f.occurred_at) >YEAR(l.first_purchase) THEN 1 ELSE 0 END) AS returning_lines
    FROM dw.fact_sales f
    JOIN dw.agg_customer_ltv l ON l.customer_key = f.customer_key
    WHERE f.is_return = 0
    GROUP BY YEAR(f.occurred_at) ORDER BY yr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_SpendDeciles
AS
BEGIN
    SET NOCOUNT ON;
    ;WITH d AS (SELECT net_spend_usd, NTILE(10) OVER (ORDER BY net_spend_usd) AS decile FROM dw.agg_customer_ltv)
    SELECT decile, COUNT(*) AS customers,
           CAST(MIN(net_spend_usd) AS DECIMAL(12,2)) AS min_spend,
           CAST(MAX(net_spend_usd) AS DECIMAL(12,2)) AS max_spend,
           CAST(SUM(net_spend_usd) AS DECIMAL(16,2)) AS total_spend
    FROM d GROUP BY decile ORDER BY decile;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_CustomerGeoHeatmap
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT c.region, c.country_code,
           COUNT(DISTINCT f.customer_key) AS buyers,
           SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_customer c ON c.customer_key = f.customer_key
    WHERE (@year IS NULL OR YEAR(f.occurred_at) = @year)
    GROUP BY c.region, c.country_code ORDER BY revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_OneAndDoneCustomers
AS
BEGIN
    SET NOCOUNT ON;
    -- share of customers who bought exactly once (churn signal)
    SELECT COUNT(*) AS total_buyers,
           SUM(CASE WHEN order_count=1 THEN 1 ELSE 0 END) AS one_and_done,
           CAST(100.0*SUM(CASE WHEN order_count=1 THEN 1 ELSE 0 END)/NULLIF(COUNT(*),0) AS DECIMAL(5,1)) AS one_and_done_pct
    FROM dw.agg_customer_ltv;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_LoyaltyVsSpend
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ISNULL(c.loyalty_tier, N'(none)') AS tier,
           COUNT(*) AS buyers,
           CAST(AVG(l.order_count*1.0) AS DECIMAL(8,2)) AS avg_orders,
           CAST(AVG(l.net_spend_usd) AS DECIMAL(12,2)) AS avg_spend_usd
    FROM dw.agg_customer_ltv l
    JOIN dw.dim_customer c ON c.customer_key = l.customer_key
    GROUP BY c.loyalty_tier ORDER BY avg_spend_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_CohortByYear
AS
BEGIN
    SET NOCOUNT ON;
    SELECT c.signup_year,
           COUNT(*) AS cohort_size,
           SUM(CASE WHEN l.customer_key IS NOT NULL THEN 1 ELSE 0 END) AS ever_bought,
           CAST(AVG(ISNULL(l.net_spend_usd,0)) AS DECIMAL(12,2)) AS avg_ltv_usd
    FROM dw.dim_customer c
    LEFT JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    GROUP BY c.signup_year ORDER BY c.signup_year;
END
GO
