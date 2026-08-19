CREATE OR ALTER PROCEDURE rpt.usp_rpt_PnL
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT year_month, revenue_usd, cogs_usd,
           revenue_usd - cogs_usd AS gross_profit_usd,
           wages_usd, severance_usd, rent_usd, other_opex_usd,
           net_income_usd, shops_active, staff_active
    FROM finance.monthly_summary
    WHERE (@year IS NULL OR LEFT(year_month,4) = CAST(@year AS CHAR(4)))
    ORDER BY year_month;
END
GO

-- wages as a % of revenue
CREATE OR ALTER PROCEDURE rpt.usp_rpt_WagesPctRevenue
AS
BEGIN
    SET NOCOUNT ON;
    SELECT LEFT(year_month,4) AS yr,
           SUM(revenue_usd) AS revenue_usd,
           SUM(wages_usd)   AS wages_usd,
           CAST(100.0 * SUM(wages_usd) / NULLIF(SUM(revenue_usd),0) AS DECIMAL(6,1)) AS wages_pct_revenue,
           SUM(staff_active)/NULLIF(COUNT(*),0) AS avg_staff_active
    FROM finance.monthly_summary
    GROUP BY LEFT(year_month,4)
    ORDER BY yr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_MarginBySegment
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT LEFT(year_month,4) AS yr,
           SUM(software_revenue_usd) AS software_rev_usd,
           SUM(hardware_revenue_usd) AS hardware_rev_usd,
           CAST(100.0*SUM(hardware_revenue_usd)/NULLIF(SUM(net_revenue_usd),0) AS DECIMAL(5,1)) AS hardware_pct
    FROM dw.agg_sales_by_month
    WHERE (@year IS NULL OR LEFT(year_month,4)=CAST(@year AS CHAR(4)))
    GROUP BY LEFT(year_month,4) ORDER BY yr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_RefundRate
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT LEFT(year_month,4) AS yr,
           SUM(gross_revenue_usd) AS gross_usd,
           SUM(-return_value_usd) AS refunds_usd,
           CAST(100.0*SUM(-return_value_usd)/NULLIF(SUM(gross_revenue_usd),0) AS DECIMAL(5,2)) AS refund_pct
    FROM dw.agg_sales_by_month
    WHERE (@year IS NULL OR LEFT(year_month,4)=CAST(@year AS CHAR(4)))
    GROUP BY LEFT(year_month,4) ORDER BY yr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_RevenueByCurrency
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT f.currency_code,
           COUNT_BIG(*) AS line_count,
           SUM(f.line_total)     AS revenue_local,
           SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    WHERE (@year IS NULL OR YEAR(f.occurred_at) = @year)
    GROUP BY f.currency_code ORDER BY revenue_usd DESC;
END
GO

-- reconcile the warehouse vs the finance close (should track closely)
CREATE OR ALTER PROCEDURE rpt.usp_rpt_WarehouseVsFinance
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ms.year_month,
           ms.revenue_usd            AS finance_revenue_usd,
           dw.net_revenue_usd        AS warehouse_net_usd,
           ms.revenue_usd - dw.net_revenue_usd AS variance_usd
    FROM finance.monthly_summary ms
    LEFT JOIN dw.agg_sales_by_month dw ON dw.year_month = ms.year_month
    ORDER BY ms.year_month;
END
GO
