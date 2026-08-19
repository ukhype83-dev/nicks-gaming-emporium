CREATE OR ALTER PROCEDURE rpt.usp_rpt_CashFlowProxy
    @from_ym CHAR(7) = '2008-01', @to_ym CHAR(7) = '2016-09'
AS
BEGIN
    SET NOCOUNT ON;
    SELECT year_month,
           revenue_usd,
           (cogs_usd + wages_usd + severance_usd + rent_usd + other_opex_usd) AS total_costs_usd,
           revenue_usd - (cogs_usd + wages_usd + severance_usd + rent_usd + other_opex_usd) AS operating_cf_usd
    FROM finance.monthly_summary
    WHERE year_month BETWEEN @from_ym AND @to_ym ORDER BY year_month;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_CostBreakdown
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT LEFT(year_month,4) AS yr,
           SUM(cogs_usd) AS cogs, SUM(wages_usd) AS wages, SUM(severance_usd) AS severance,
           SUM(rent_usd) AS rent, SUM(other_opex_usd) AS other_opex
    FROM finance.monthly_summary
    WHERE (@year IS NULL OR LEFT(year_month,4)=CAST(@year AS CHAR(4)))
    GROUP BY LEFT(year_month,4) ORDER BY yr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_CurrencyExposure
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT f.currency_code,
           COUNT_BIG(*) AS lines,
           SUM(f.line_total_usd) AS revenue_usd,
           CAST(100.0*SUM(f.line_total_usd)/SUM(SUM(f.line_total_usd)) OVER () AS DECIMAL(5,2)) AS pct_of_usd
    FROM dw.fact_sales f
    WHERE f.is_return=0 AND (@year IS NULL OR YEAR(f.occurred_at)=@year)
    GROUP BY f.currency_code ORDER BY revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_ProfitableMonths
AS
BEGIN
    SET NOCOUNT ON;
    SELECT year_month, net_income_usd,
           CASE WHEN net_income_usd >= 0 THEN N'profit' ELSE N'loss' END AS result
    FROM finance.monthly_summary
    ORDER BY year_month;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_TaxCollected
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT YEAR(f.occurred_at) AS yr,
           CAST(SUM(f.line_tax / COALESCE(NULLIF(fx.rate_to_usd,0),1.0)) AS DECIMAL(16,2)) AS tax_usd
    FROM dw.fact_sales f
    LEFT JOIN dw.dim_fx fx ON fx.currency_code=f.currency_code AND fx.year=YEAR(f.occurred_at)
    WHERE f.is_return=0 AND (@year IS NULL OR YEAR(f.occurred_at)=@year)
    GROUP BY YEAR(f.occurred_at) ORDER BY yr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_StoreContribution
    @year SMALLINT = 2008, @top INT = 25
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) s.shop_key, s.name, s.country_code,
           SUM(a.revenue_usd) AS revenue_usd, SUM(a.tx_count) AS transactions
    FROM dw.agg_sales_by_day_shop a
    JOIN dw.dim_shop s ON s.shop_key = a.shop_key
    WHERE a.date_key BETWEEN @year*10000 AND @year*10000+1231
    GROUP BY s.shop_key, s.name, s.country_code
    ORDER BY revenue_usd DESC;
END
GO
