CREATE OR ALTER PROCEDURE rpt.usp_rpt_ProductDetail
    @product_key BIGINT
AS
BEGIN
    SET NOCOUNT ON;
    SELECT p.product_key, p.product_kind, p.title, p.platform_name, p.category,
           p.publisher, p.developer, p.first_release_date,
           pp.units_sold, pp.revenue_usd, pp.return_count, pp.distinct_customers,
           pp.first_sold, pp.last_sold
    FROM dw.dim_product p
    LEFT JOIN dw.agg_product_performance pp ON pp.product_key = p.product_key
    WHERE p.product_key = @product_key;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_SlowMovers
    @max_units BIGINT = 5, @product_kind NVARCHAR(12) = 'game'
AS
BEGIN
    SET NOCOUNT ON;
    SELECT p.product_key, p.title, p.platform_name, pp.units_sold, pp.revenue_usd, pp.last_sold
    FROM dw.agg_product_performance pp
    JOIN dw.dim_product p ON p.product_key = pp.product_key
    WHERE pp.units_sold <= @max_units AND (@product_kind IS NULL OR p.product_kind = @product_kind)
    ORDER BY pp.units_sold, pp.revenue_usd;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_GenreBreakdown
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ISNULL(p.category, N'(uncategorised)') AS genre,
           COUNT_BIG(*) AS line_count,
           SUM(CASE WHEN f.is_return=0 THEN f.quantity ELSE 0 END) AS units,
           SUM(f.line_total_usd) AS net_revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE p.product_kind = 'game' AND (@year IS NULL OR YEAR(f.occurred_at) = @year)
    GROUP BY p.category ORDER BY net_revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_PublisherLeaderboard
    @top INT = 25, @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) ISNULL(p.publisher,N'(unknown)') AS publisher,
           COUNT(DISTINCT p.product_key) AS titles,
           SUM(CASE WHEN f.is_return=0 THEN f.quantity ELSE 0 END) AS units,
           SUM(f.line_total_usd) AS net_revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE (@year IS NULL OR YEAR(f.occurred_at) = @year)
    GROUP BY p.publisher ORDER BY net_revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_PlatformLifecycle
    @platform_name NVARCHAR(100)
AS
BEGIN
    SET NOCOUNT ON;
    SELECT year_month, SUM(units) AS units, SUM(revenue_usd) AS revenue_usd
    FROM dw.agg_sales_by_month_platform
    WHERE platform_name = @platform_name
    GROUP BY year_month ORDER BY year_month;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_TradeInVelocity
    @top INT = 25
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) p.product_key, p.title, p.platform_name,
           COUNT_BIG(*) AS trade_ins, SUM(t.offer_amount_usd) AS total_offered_usd
    FROM dw.fact_tradein t
    JOIN dw.dim_product p ON p.product_key = t.product_key
    GROUP BY p.product_key, p.title, p.platform_name
    ORDER BY trade_ins DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_HardwareAttachRate
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    -- software units vs hardware units by platform (attach = games per console)
    SELECT pl.platform_name,
           SUM(CASE WHEN f.is_hardware=1 AND f.is_return=0 THEN f.quantity ELSE 0 END) AS hardware_units,
           SUM(CASE WHEN f.is_hardware=0 AND f.is_return=0 THEN f.quantity ELSE 0 END) AS software_units,
           CAST(1.0*SUM(CASE WHEN f.is_hardware=0 AND f.is_return=0 THEN f.quantity ELSE 0 END)
                / NULLIF(SUM(CASE WHEN f.is_hardware=1 AND f.is_return=0 THEN f.quantity ELSE 0 END),0)
                AS DECIMAL(8,2)) AS attach_rate
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    JOIN dw.dim_platform pl ON pl.platform_key = p.platform_key
    WHERE (@year IS NULL OR YEAR(f.occurred_at) = @year)
    GROUP BY pl.platform_name
    HAVING SUM(CASE WHEN f.is_hardware=1 THEN 1 ELSE 0 END) > 0
    ORDER BY attach_rate DESC;
END
GO
