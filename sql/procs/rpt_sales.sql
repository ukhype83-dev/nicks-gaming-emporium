CREATE OR ALTER PROCEDURE rpt.usp_rpt_SalesByMonth
    @from_ym CHAR(7) = '1986-01', @to_ym CHAR(7) = '2016-12'
AS
BEGIN
    SET NOCOUNT ON;
    SELECT year_month, tx_count, units, gross_revenue_usd, return_value_usd,
           net_revenue_usd, software_revenue_usd, hardware_revenue_usd, avg_line_usd
    FROM dw.agg_sales_by_month
    WHERE year_month BETWEEN @from_ym AND @to_ym
    ORDER BY year_month;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_SalesByPlatform
    @from_ym CHAR(7) = '1986-01', @to_ym CHAR(7) = '2016-12'
AS
BEGIN
    SET NOCOUNT ON;
    SELECT platform_name,
           SUM(units) AS units, SUM(line_count) AS line_count, SUM(revenue_usd) AS revenue_usd
    FROM dw.agg_sales_by_month_platform
    WHERE year_month BETWEEN @from_ym AND @to_ym
    GROUP BY platform_name
    ORDER BY revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_TopSellers
    @top INT = 25, @product_kind NVARCHAR(12) = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) p.product_key, p.product_kind, p.title, p.platform_name,
           pp.units_sold, pp.revenue_usd, pp.distinct_customers, pp.first_sold, pp.last_sold
    FROM dw.agg_product_performance pp
    JOIN dw.dim_product p ON p.product_key = pp.product_key
    WHERE (@product_kind IS NULL OR p.product_kind = @product_kind)
    ORDER BY pp.revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_SalesByShop
    @from_key INT = 19860101, @to_key INT = 20161231, @top INT = 50
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) s.shop_key, s.shop_code, s.name, s.country_code, s.city,
           SUM(a.tx_count) AS tx_count, SUM(a.units) AS units, SUM(a.revenue_usd) AS revenue_usd
    FROM dw.agg_sales_by_day_shop a
    JOIN dw.dim_shop s ON s.shop_key = a.shop_key
    WHERE a.date_key BETWEEN @from_key AND @to_key
    GROUP BY s.shop_key, s.shop_code, s.name, s.country_code, s.city
    ORDER BY revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_SalesByRegion
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT g.region,
           COUNT_BIG(*) AS line_count,
           SUM(CASE WHEN f.is_return=0 THEN f.quantity ELSE 0 END) AS units,
           SUM(f.line_total_usd) AS net_revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_shop s ON s.shop_key = f.shop_key
    JOIN dw.dim_geography g ON g.country_code = s.country_code
    WHERE (@year IS NULL OR YEAR(f.occurred_at) = @year)
    GROUP BY g.region
    ORDER BY net_revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_ChannelMix
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ch.channel_name,
           COUNT(DISTINCT f.transaction_id) AS tx_count,
           SUM(f.line_total_usd) AS net_revenue_usd,
           CAST(100.0 * SUM(f.line_total_usd) / NULLIF(SUM(SUM(f.line_total_usd)) OVER (), 0) AS DECIMAL(5,2)) AS pct
    FROM dw.fact_sales f
    JOIN dw.dim_channel ch ON ch.channel_key = f.channel_key
    WHERE (@year IS NULL OR YEAR(f.occurred_at) = @year)
    GROUP BY ch.channel_name
    ORDER BY net_revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_NewVsUsedSplit
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT co.condition_name, co.is_used,
           COUNT_BIG(*) AS line_count,
           SUM(CASE WHEN f.is_return=0 THEN f.quantity ELSE 0 END) AS units,
           SUM(f.line_total_usd) AS net_revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_condition co ON co.condition_key = f.condition_key
    WHERE (@year IS NULL OR YEAR(f.occurred_at) = @year)
    GROUP BY co.condition_name, co.is_used
    ORDER BY net_revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_YoYGrowth
AS
BEGIN
    SET NOCOUNT ON;
    ;WITH yearly AS (
        SELECT LEFT(year_month,4) AS yr, SUM(net_revenue_usd) AS rev
        FROM dw.agg_sales_by_month GROUP BY LEFT(year_month,4)
    )
    SELECT yr, rev,
           LAG(rev) OVER (ORDER BY yr) AS prior_rev,
           CAST(100.0*(rev - LAG(rev) OVER (ORDER BY yr)) / NULLIF(LAG(rev) OVER (ORDER BY yr),0) AS DECIMAL(6,1)) AS yoy_pct
    FROM yearly ORDER BY yr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_SeasonalPattern
AS
BEGIN
    SET NOCOUNT ON;
    SELECT RIGHT(year_month,2) AS month_num,
           SUM(net_revenue_usd) AS total_net_usd,
           CAST(AVG(net_revenue_usd) AS DECIMAL(16,2)) AS avg_month_net_usd
    FROM dw.agg_sales_by_month
    GROUP BY RIGHT(year_month,2) ORDER BY month_num;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_DailySalesForShop
    @shop_key BIGINT, @from_key INT = 19860101, @to_key INT = 20161231
AS
BEGIN
    SET NOCOUNT ON;
    SELECT a.date_key, d.full_date, a.tx_count, a.units, a.revenue_usd
    FROM dw.agg_sales_by_day_shop a
    JOIN dw.dim_date d ON d.date_key = a.date_key
    WHERE a.shop_key = @shop_key AND a.date_key BETWEEN @from_key AND @to_key
    ORDER BY a.date_key;
END
GO
