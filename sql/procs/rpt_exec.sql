-- company health over the company's whole life
CREATE OR ALTER PROCEDURE rpt.usp_rpt_CompanyHealth
AS
BEGIN
    SET NOCOUNT ON;
    SELECT LEFT(year_month,4) AS yr,
           CAST(SUM(revenue_usd)/1e6 AS DECIMAL(12,1)) AS revenue_musd,
           CAST(SUM(revenue_usd - cogs_usd)/1e6 AS DECIMAL(12,1)) AS gross_profit_musd,
           CAST(SUM(net_income_usd)/1e6 AS DECIMAL(12,1)) AS net_income_musd,
           CAST(100.0*SUM(wages_usd)/NULLIF(SUM(revenue_usd),0) AS DECIMAL(6,1)) AS wages_pct_rev,
           MAX(shops_active) AS shops, MAX(staff_active) AS staff
    FROM finance.monthly_summary
    GROUP BY LEFT(year_month,4) ORDER BY yr;
END
GO

-- one year on a page: revenue, margin, mix, headcount, top platform
CREATE OR ALTER PROCEDURE rpt.usp_rpt_ExecutiveDashboard
    @year SMALLINT = 2008
AS
BEGIN
    SET NOCOUNT ON;
    SELECT
        @year AS year,
        (SELECT CAST(SUM(revenue_usd)/1e6 AS DECIMAL(12,1)) FROM finance.monthly_summary WHERE LEFT(year_month,4)=CAST(@year AS CHAR(4))) AS revenue_musd,
        (SELECT CAST(SUM(net_income_usd)/1e6 AS DECIMAL(12,1)) FROM finance.monthly_summary WHERE LEFT(year_month,4)=CAST(@year AS CHAR(4))) AS net_income_musd,
        (SELECT CAST(100.0*SUM(wages_usd)/NULLIF(SUM(revenue_usd),0) AS DECIMAL(6,1)) FROM finance.monthly_summary WHERE LEFT(year_month,4)=CAST(@year AS CHAR(4))) AS wages_pct,
        (SELECT CAST(100.0*SUM(hardware_revenue_usd)/NULLIF(SUM(net_revenue_usd),0) AS DECIMAL(5,1)) FROM dw.agg_sales_by_month WHERE LEFT(year_month,4)=CAST(@year AS CHAR(4))) AS hardware_pct,
        (SELECT COUNT(*) FROM dw.dim_shop WHERE opened_date <= DATEFROMPARTS(@year,12,31) AND (closed_date IS NULL OR closed_date >= DATEFROMPARTS(@year,1,1))) AS shops_active,
        (SELECT TOP 1 platform_name FROM dw.agg_sales_by_month_platform WHERE LEFT(year_month,4)=CAST(@year AS CHAR(4)) GROUP BY platform_name ORDER BY SUM(revenue_usd) DESC) AS top_platform;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_KPISnapshot
    @year_month CHAR(7) = '2008-12'
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ms.year_month, ms.revenue_usd, ms.cogs_usd, ms.net_income_usd,
           ms.shops_active, ms.staff_active,
           dw.units, dw.tx_count, dw.return_count, dw.avg_line_usd
    FROM finance.monthly_summary ms
    LEFT JOIN dw.agg_sales_by_month dw ON dw.year_month = ms.year_month
    WHERE ms.year_month = @year_month;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_StoreScorecard
    @shop_key BIGINT, @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT s.shop_key, s.shop_code, s.name, s.country_code, s.city, s.is_open,
           SUM(a.tx_count) AS transactions, SUM(a.units) AS units,
           SUM(a.revenue_usd) AS revenue_usd,
           CAST(SUM(a.revenue_usd)/NULLIF(SUM(a.tx_count),0) AS DECIMAL(12,2)) AS avg_basket_usd
    FROM dw.dim_shop s
    LEFT JOIN dw.agg_sales_by_day_shop a ON a.shop_key = s.shop_key
        AND (@year IS NULL OR a.date_key BETWEEN @year*10000 AND @year*10000+1231)
    WHERE s.shop_key = @shop_key
    GROUP BY s.shop_key, s.shop_code, s.name, s.country_code, s.city, s.is_open;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_TopAndBottomShops
    @year SMALLINT = 2008, @n INT = 10
AS
BEGIN
    SET NOCOUNT ON;
    ;WITH shop_rev AS (
        SELECT s.shop_key, s.name, s.country_code, SUM(a.revenue_usd) AS rev
        FROM dw.agg_sales_by_day_shop a
        JOIN dw.dim_shop s ON s.shop_key = a.shop_key
        WHERE a.date_key BETWEEN @year*10000 AND @year*10000+1231
        GROUP BY s.shop_key, s.name, s.country_code
    )
    SELECT 'TOP' AS band, name, country_code, rev FROM (SELECT TOP (@n) * FROM shop_rev ORDER BY rev DESC) t
    UNION ALL
    SELECT 'BOTTOM', name, country_code, rev FROM (SELECT TOP (@n) * FROM shop_rev ORDER BY rev ASC) b
    ORDER BY band, rev DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_MonthlyPnLTrend
    @from_ym CHAR(7) = '2010-01', @to_ym CHAR(7) = '2016-09'
AS
BEGIN
    SET NOCOUNT ON;
    SELECT year_month, revenue_usd, cogs_usd, wages_usd, rent_usd, other_opex_usd,
           net_income_usd,
           CAST(100.0*(revenue_usd-cogs_usd)/NULLIF(revenue_usd,0) AS DECIMAL(5,1)) AS gross_margin_pct
    FROM finance.monthly_summary
    WHERE year_month BETWEEN @from_ym AND @to_ym
    ORDER BY year_month;
END
GO
