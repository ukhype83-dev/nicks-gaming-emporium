CREATE OR ALTER PROCEDURE rpt.usp_rpt_HeadcountByYear
AS
BEGIN
    SET NOCOUNT ON;
    SELECT LEFT(year_month,4) AS yr,
           MAX(staff_active) AS peak_staff,
           MIN(staff_active) AS trough_staff,
           MAX(shops_active) AS peak_shops
    FROM finance.monthly_summary
    GROUP BY LEFT(year_month,4) ORDER BY yr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_PayrollSummary
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT YEAR(pr.paid_at) AS yr, MONTH(pr.paid_at) AS mo,
           COUNT(*) AS payslips,
           CAST(SUM(pl.gross / COALESCE(NULLIF(fx.rate_to_usd,0),1.0)) AS DECIMAL(16,2)) AS gross_usd,
           CAST(SUM(pl.net   / COALESCE(NULLIF(fx.rate_to_usd,0),1.0)) AS DECIMAL(16,2)) AS net_usd
    FROM hr.payroll_lines pl
    JOIN hr.payroll_runs pr ON pr.payroll_run_id = pl.payroll_run_id
    LEFT JOIN dw.dim_fx fx ON fx.currency_code = pr.currency_code AND fx.year = YEAR(pr.paid_at)
    WHERE pr.status = 'posted' AND (@year IS NULL OR YEAR(pr.paid_at) = @year)
    GROUP BY YEAR(pr.paid_at), MONTH(pr.paid_at)
    ORDER BY yr, mo;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_TenureDistribution
    @as_of DATE = '2016-09-30'
AS
BEGIN
    SET NOCOUNT ON;
    ;WITH t AS (
        SELECT DATEDIFF(MONTH, started_at, ISNULL(ended_at, @as_of)) AS months
        FROM hr.employment_spells WHERE started_at <= @as_of
    )
    SELECT CASE WHEN months < 6 THEN N'0-6m' WHEN months < 12 THEN N'6-12m'
                WHEN months < 36 THEN N'1-3y' WHEN months < 60 THEN N'3-5y'
                ELSE N'5y+' END AS tenure_band,
           COUNT(*) AS spells
    FROM t GROUP BY CASE WHEN months < 6 THEN N'0-6m' WHEN months < 12 THEN N'6-12m'
                WHEN months < 36 THEN N'1-3y' WHEN months < 60 THEN N'3-5y' ELSE N'5y+' END
    ORDER BY MIN(months);
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_RoleDistribution
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ISNULL(role_title, N'(unknown)') AS role_title,
           COUNT(*) AS spells,
           SUM(CASE WHEN ended_on IS NULL THEN 1 ELSE 0 END) AS still_active
    FROM dw.dim_staff GROUP BY role_title ORDER BY spells DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_Attrition
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT YEAR(ended_at) AS yr, ISNULL(termination_reason, N'(unspecified)') AS reason,
           COUNT(*) AS leavers
    FROM hr.employment_spells
    WHERE ended_at IS NOT NULL AND (@year IS NULL OR YEAR(ended_at) = @year)
    GROUP BY YEAR(ended_at), termination_reason
    ORDER BY yr, leavers DESC;
END
GO

-- persons 1-9 are the leadership; change_reason carries the retention/severance/winddown lumps
CREATE OR ALTER PROCEDURE rpt.usp_rpt_ExecCompensation
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ch.person_id,
           LTRIM(RTRIM(COALESCE(p.first_name,N'') + N' ' + COALESCE(p.last_name,N''))) AS name,
           ch.effective_from, ch.change_reason,
           CAST(ch.annual_wage / COALESCE(NULLIF(fx.rate_to_usd,0),1.0) AS DECIMAL(14,2)) AS annual_wage_usd
    FROM hr.compensation_history ch
    JOIN hr.persons p ON p.person_id = ch.person_id
    LEFT JOIN dw.dim_fx fx ON fx.currency_code = ch.currency_code AND fx.year = YEAR(ch.effective_from)
    WHERE ch.person_id <= 9
    ORDER BY ch.person_id, ch.effective_from;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_SalesPerStaff
    @year SMALLINT = NULL, @top INT = 25
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) st.staff_key, st.full_name, st.role_title, st.home_shop_id,
           COUNT(DISTINCT f.transaction_id) AS transactions,
           SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_staff st ON st.staff_key = f.staff_key
    WHERE (@year IS NULL OR YEAR(f.occurred_at) = @year)
    GROUP BY st.staff_key, st.full_name, st.role_title, st.home_shop_id
    ORDER BY revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_WageBillByCountry
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT pr.country_code,
           CAST(SUM(pl.gross / COALESCE(NULLIF(fx.rate_to_usd,0),1.0)) AS DECIMAL(16,2)) AS wage_bill_usd,
           COUNT(DISTINCT pl.spell_id) AS staff
    FROM hr.payroll_lines pl
    JOIN hr.payroll_runs pr ON pr.payroll_run_id = pl.payroll_run_id
    LEFT JOIN dw.dim_fx fx ON fx.currency_code = pr.currency_code AND fx.year = YEAR(pr.paid_at)
    WHERE pr.status='posted' AND (@year IS NULL OR YEAR(pr.paid_at) = @year)
    GROUP BY pr.country_code ORDER BY wage_bill_usd DESC;
END
GO
