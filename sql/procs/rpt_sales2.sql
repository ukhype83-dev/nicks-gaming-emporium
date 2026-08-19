CREATE OR ALTER PROCEDURE rpt.usp_rpt_SalesByWeekday
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT d.day_of_week, d.day_name,
           COUNT_BIG(*) AS lines, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_date d ON d.date_key = f.date_key
    WHERE (@year IS NULL OR d.year = @year)
    GROUP BY d.day_of_week, d.day_name ORDER BY d.day_of_week;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_BasketAnalysis
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    ;WITH baskets AS (
        SELECT transaction_id, COUNT(*) AS lines, SUM(line_total_usd) AS basket_usd
        FROM dw.fact_sales
        WHERE (@year IS NULL OR YEAR(occurred_at) = @year) AND is_return = 0
        GROUP BY transaction_id
    )
    SELECT CASE WHEN lines=1 THEN N'1' WHEN lines=2 THEN N'2' WHEN lines<=4 THEN N'3-4'
                WHEN lines<=8 THEN N'5-8' ELSE N'9+' END AS basket_size,
           COUNT(*) AS baskets, CAST(AVG(basket_usd) AS DECIMAL(12,2)) AS avg_basket_usd
    FROM baskets
    GROUP BY CASE WHEN lines=1 THEN N'1' WHEN lines=2 THEN N'2' WHEN lines<=4 THEN N'3-4'
                  WHEN lines<=8 THEN N'5-8' ELSE N'9+' END
    ORDER BY MIN(lines);
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_DiscountAnalysis
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT YEAR(occurred_at) AS yr,
           COUNT_BIG(*) AS lines,
           SUM(CASE WHEN line_discount > 0 THEN 1 ELSE 0 END) AS discounted_lines,
           CAST(100.0*SUM(CASE WHEN line_discount>0 THEN 1 ELSE 0 END)/COUNT_BIG(*) AS DECIMAL(5,1)) AS discounted_pct,
           CAST(SUM(line_discount)/COALESCE(NULLIF(SUM(line_total+line_discount),0),1) *100 AS DECIMAL(5,1)) AS avg_discount_depth_pct
    FROM dw.fact_sales
    WHERE is_return = 0 AND (@year IS NULL OR YEAR(occurred_at) = @year)
    GROUP BY YEAR(occurred_at) ORDER BY yr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_PriceBands
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT CASE WHEN unit_price < 10 THEN N'<$10' WHEN unit_price < 25 THEN N'$10-25'
                WHEN unit_price < 50 THEN N'$25-50' WHEN unit_price < 100 THEN N'$50-100'
                ELSE N'$100+' END AS price_band,
           COUNT_BIG(*) AS lines, SUM(quantity) AS units, SUM(line_total_usd) AS revenue_usd
    FROM dw.fact_sales
    WHERE is_return = 0 AND is_hardware = 0 AND (@year IS NULL OR YEAR(occurred_at) = @year)
    GROUP BY CASE WHEN unit_price < 10 THEN N'<$10' WHEN unit_price < 25 THEN N'$10-25'
                  WHEN unit_price < 50 THEN N'$25-50' WHEN unit_price < 100 THEN N'$50-100'
                  ELSE N'$100+' END
    ORDER BY MIN(unit_price);
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_HardwareVsSoftwareTrend
AS
BEGIN
    SET NOCOUNT ON;
    SELECT LEFT(year_month,4) AS yr,
           SUM(software_revenue_usd) AS software_usd,
           SUM(hardware_revenue_usd) AS hardware_usd,
           CAST(100.0*SUM(hardware_revenue_usd)/NULLIF(SUM(net_revenue_usd),0) AS DECIMAL(5,1)) AS hardware_pct
    FROM dw.agg_sales_by_month GROUP BY LEFT(year_month,4) ORDER BY yr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_FireSaleImpact
AS
BEGIN
    SET NOCOUNT ON;
    -- sales during the fire-sale window vs before
    SELECT d.is_fire_sale, d.era,
           COUNT_BIG(*) AS lines, SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd,
           CAST(AVG(f.line_discount) AS DECIMAL(10,2)) AS avg_discount
    FROM dw.fact_sales f
    JOIN dw.dim_date d ON d.date_key = f.date_key
    WHERE d.year = 2016 AND f.is_return = 0
    GROUP BY d.is_fire_sale, d.era ORDER BY d.is_fire_sale;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_ChannelGrowth
AS
BEGIN
    SET NOCOUNT ON;
    SELECT YEAR(f.occurred_at) AS yr, ch.channel_name,
           SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_channel ch ON ch.channel_key = f.channel_key
    WHERE f.is_return = 0
    GROUP BY YEAR(f.occurred_at), ch.channel_name
    ORDER BY yr, revenue_usd DESC;
END
GO
