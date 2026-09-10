CREATE OR ALTER PROCEDURE rpt.usp_rpt_SalesByQuarter AS
BEGIN SET NOCOUNT ON;
    SELECT d.year, d.quarter, SUM(f.line_total_usd) AS revenue_usd, SUM(f.quantity) AS units
    FROM dw.fact_sales f JOIN dw.dim_date d ON d.date_key=f.date_key
    WHERE f.is_return=0 GROUP BY d.year, d.quarter ORDER BY d.year, d.quarter;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_SalesByEra AS
BEGIN SET NOCOUNT ON;
    SELECT d.era, SUM(f.line_total_usd) AS revenue_usd, SUM(f.quantity) AS units, COUNT_BIG(*) AS lines
    FROM dw.fact_sales f JOIN dw.dim_date d ON d.date_key=f.date_key
    GROUP BY d.era ORDER BY MIN(d.date_key);
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_MonthOverMonth @from_ym CHAR(7)='2007-01', @to_ym CHAR(7)='2009-12' AS
BEGIN SET NOCOUNT ON;
    SELECT year_month, net_revenue_usd,
           LAG(net_revenue_usd) OVER (ORDER BY year_month) AS prev_month,
           CAST(100.0*(net_revenue_usd-LAG(net_revenue_usd) OVER (ORDER BY year_month))
                /NULLIF(LAG(net_revenue_usd) OVER (ORDER BY year_month),0) AS DECIMAL(6,1)) AS mom_pct
    FROM dw.agg_sales_by_month WHERE year_month BETWEEN @from_ym AND @to_ym ORDER BY year_month;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_HolidaySeasonSales AS
BEGIN SET NOCOUNT ON;
    SELECT d.year, d.is_holiday_season, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_date d ON d.date_key=f.date_key
    WHERE f.is_return=0 GROUP BY d.year, d.is_holiday_season ORDER BY d.year, d.is_holiday_season;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_WeekendVsWeekday @year SMALLINT=NULL AS
BEGIN SET NOCOUNT ON;
    SELECT d.is_weekend, COUNT_BIG(*) AS lines, SUM(f.line_total_usd) AS revenue_usd,
           CAST(AVG(f.line_total_usd) AS DECIMAL(12,2)) AS avg_line_usd
    FROM dw.fact_sales f JOIN dw.dim_date d ON d.date_key=f.date_key
    WHERE (@year IS NULL OR d.year=@year) GROUP BY d.is_weekend;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_UnitsByYear AS
BEGIN SET NOCOUNT ON;
    SELECT YEAR(occurred_at) AS yr,
           SUM(CASE WHEN is_hardware=0 THEN quantity ELSE 0 END) AS software_units,
           SUM(CASE WHEN is_hardware=1 THEN quantity ELSE 0 END) AS hardware_units
    FROM dw.fact_sales WHERE is_return=0 GROUP BY YEAR(occurred_at) ORDER BY yr;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_TopShopsAllTime @top INT=50 AS
BEGIN SET NOCOUNT ON;
    SELECT TOP (@top) s.name, s.country_code, s.city, SUM(a.revenue_usd) AS lifetime_usd
    FROM dw.agg_sales_by_day_shop a JOIN dw.dim_shop s ON s.shop_key=a.shop_key
    GROUP BY s.name, s.country_code, s.city ORDER BY lifetime_usd DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_RevenuePerShopByYear AS
BEGIN SET NOCOUNT ON;
    SELECT LEFT(CAST(a.date_key AS CHAR(8)),4) AS yr,
           COUNT(DISTINCT a.shop_key) AS shops,
           SUM(a.revenue_usd) AS revenue_usd,
           CAST(SUM(a.revenue_usd)/NULLIF(COUNT(DISTINCT a.shop_key),0) AS DECIMAL(14,2)) AS rev_per_shop
    FROM dw.agg_sales_by_day_shop a
    GROUP BY LEFT(CAST(a.date_key AS CHAR(8)),4) ORDER BY yr;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_GenreByYear @year SMALLINT=2008 AS
BEGIN SET NOCOUNT ON;
    SELECT ISNULL(p.category,N'(uncat)') AS genre, SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key=f.product_key
    WHERE p.product_kind='game' AND YEAR(f.occurred_at)=@year AND f.is_return=0
    GROUP BY p.category ORDER BY revenue_usd DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_AvgBasketByYear AS
BEGIN SET NOCOUNT ON;
    SELECT LEFT(year_month,4) AS yr,
           SUM(tx_count) AS transactions,
           CAST(SUM(net_revenue_usd)/NULLIF(SUM(tx_count),0) AS DECIMAL(12,2)) AS avg_basket_usd
    FROM dw.agg_sales_by_month GROUP BY LEFT(year_month,4) ORDER BY yr;
END
GO
