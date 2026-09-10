CREATE OR ALTER PROCEDURE rpt.usp_rpt_DailyClose
    @date DATE
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @dk INT = CONVERT(INT, CONVERT(CHAR(8), @date, 112));
    SELECT SUM(a.tx_count) AS transactions, SUM(a.units) AS units,
           SUM(a.revenue_usd) AS revenue_usd, COUNT(DISTINCT a.shop_key) AS shops_trading
    FROM dw.agg_sales_by_day_shop a WHERE a.date_key = @dk;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_ShopDayReport
    @shop_key BIGINT, @date DATE
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ch.channel_name,
           COUNT(DISTINCT f.transaction_id) AS transactions,
           SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_channel ch ON ch.channel_key = f.channel_key
    WHERE f.shop_key = @shop_key AND CAST(f.occurred_at AS DATE) = @date
    GROUP BY ch.channel_name ORDER BY revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_SalesByHour
    @date DATE
AS
BEGIN
    SET NOCOUNT ON;
    SELECT DATEPART(HOUR, occurred_at) AS hr,
           COUNT(*) AS lines, SUM(line_total_usd) AS revenue_usd
    FROM dw.fact_sales
    WHERE CAST(occurred_at AS DATE) = @date
    GROUP BY DATEPART(HOUR, occurred_at) ORDER BY hr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_TopSellersForDay
    @date DATE, @top INT = 20
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) p.title, p.platform_name, SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE CAST(f.occurred_at AS DATE) = @date AND f.is_return = 0
    GROUP BY p.title, p.platform_name ORDER BY units DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_RefundsForDay
    @date DATE
AS
BEGIN
    SET NOCOUNT ON;
    SELECT p.title, p.platform_name, COUNT(*) AS refund_lines, SUM(-f.line_total_usd) AS refund_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE CAST(f.occurred_at AS DATE) = @date AND f.is_return = 1
    GROUP BY p.title, p.platform_name ORDER BY refund_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_ReturnsRate
    @from_ym CHAR(7), @to_ym CHAR(7)
AS
BEGIN
    SET NOCOUNT ON;
    SELECT year_month,
           gross_revenue_usd, return_count,
           CAST(-return_value_usd AS DECIMAL(16,2)) AS refunds_usd,
           CAST(100.0*(-return_value_usd)/NULLIF(gross_revenue_usd,0) AS DECIMAL(5,2)) AS return_pct
    FROM dw.agg_sales_by_month WHERE year_month BETWEEN @from_ym AND @to_ym ORDER BY year_month;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_NewCustomersForDay
    @date DATE
AS
BEGIN
    SET NOCOUNT ON;
    SELECT c.country_code, COUNT(*) AS new_customers
    FROM dw.dim_customer c WHERE c.signup_date = @date
    GROUP BY c.country_code ORDER BY new_customers DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_ChannelSalesForDay
    @date DATE
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ch.channel_name, COUNT(DISTINCT f.transaction_id) AS transactions, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_channel ch ON ch.channel_key = f.channel_key
    WHERE CAST(f.occurred_at AS DATE) = @date
    GROUP BY ch.channel_name ORDER BY revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_WeekInReview
    @week_ending DATE
AS
BEGIN
    SET NOCOUNT ON;
    SELECT CAST(occurred_at AS DATE) AS day, COUNT(*) AS lines, SUM(line_total_usd) AS revenue_usd
    FROM dw.fact_sales
    WHERE occurred_at >= DATEADD(DAY,-6,@week_ending) AND occurred_at < DATEADD(DAY,1,@week_ending)
    GROUP BY CAST(occurred_at AS DATE) ORDER BY day;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_ShopRankForDay
    @date DATE, @top INT = 25
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @dk INT = CONVERT(INT, CONVERT(CHAR(8), @date, 112));
    SELECT TOP (@top) s.name, s.country_code, a.tx_count, a.units, a.revenue_usd
    FROM dw.agg_sales_by_day_shop a JOIN dw.dim_shop s ON s.shop_key = a.shop_key
    WHERE a.date_key = @dk ORDER BY a.revenue_usd DESC;
END
GO
