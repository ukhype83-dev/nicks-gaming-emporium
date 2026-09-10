/* =============================================================
   rpt — HR / people reports (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_HeadcountByYear()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT LEFT(year_month,4) AS yr,
           MAX(staff_active) AS peak_staff,
           MIN(staff_active) AS trough_staff,
           MAX(shops_active) AS peak_shops
    FROM finance.monthly_summary
    GROUP BY LEFT(year_month,4) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_PayrollSummary(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT extract(year from pr.paid_at) AS yr, extract(month from pr.paid_at) AS mo,
           COUNT(*) AS payslips,
           CAST(SUM(pl.gross / COALESCE(NULLIF(fx.rate_to_usd,0),1.0)) AS decimal(16,2)) AS gross_usd,
           CAST(SUM(pl.net   / COALESCE(NULLIF(fx.rate_to_usd,0),1.0)) AS decimal(16,2)) AS net_usd
    FROM hr.payroll_lines pl
    JOIN hr.payroll_runs pr ON pr.payroll_run_id = pl.payroll_run_id
    LEFT JOIN dw.dim_fx fx ON fx.currency_code = pr.currency_code AND fx.year = extract(year from pr.paid_at)::smallint
    WHERE pr.status = 'posted' AND (p_year IS NULL OR extract(year from pr.paid_at) = p_year)
    GROUP BY extract(year from pr.paid_at), extract(month from pr.paid_at)
    ORDER BY yr, mo;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TenureDistribution(p_as_of date DEFAULT '2016-09-30')
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    WITH t AS (
        SELECT ((extract(year from COALESCE(ended_at, p_as_of)) - extract(year from started_at))*12
              + (extract(month from COALESCE(ended_at, p_as_of)) - extract(month from started_at)))::int AS months
        FROM hr.employment_spells WHERE started_at <= p_as_of
    )
    SELECT CASE WHEN months < 6 THEN '0-6m' WHEN months < 12 THEN '6-12m'
                WHEN months < 36 THEN '1-3y' WHEN months < 60 THEN '3-5y'
                ELSE '5y+' END AS tenure_band,
           COUNT(*) AS spells
    FROM t GROUP BY CASE WHEN months < 6 THEN '0-6m' WHEN months < 12 THEN '6-12m'
                WHEN months < 36 THEN '1-3y' WHEN months < 60 THEN '3-5y' ELSE '5y+' END
    ORDER BY MIN(months);
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_RoleDistribution()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT COALESCE(role_title, '(unknown)') AS role_title,
           COUNT(*) AS spells,
           SUM(CASE WHEN ended_on IS NULL THEN 1 ELSE 0 END) AS still_active
    FROM dw.dim_staff GROUP BY role_title ORDER BY spells DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_Attrition(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT extract(year from ended_at) AS yr, COALESCE(termination_reason, '(unspecified)') AS reason,
           COUNT(*) AS leavers
    FROM hr.employment_spells
    WHERE ended_at IS NOT NULL AND (p_year IS NULL OR extract(year from ended_at) = p_year)
    GROUP BY extract(year from ended_at), termination_reason
    ORDER BY yr, leavers DESC;
    RETURN c;
END $$;

/* The exec-pay story: persons 1-9 are the leadership; change_reason carries
   the retention/severance/winddown lumps of the collapse. */
CREATE OR REPLACE FUNCTION rpt.usp_rpt_ExecCompensation()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT ch.person_id,
           TRIM(COALESCE(p.first_name,'') || ' ' || COALESCE(p.last_name,'')) AS name,
           ch.effective_from, ch.change_reason,
           CAST(ch.annual_wage / COALESCE(NULLIF(fx.rate_to_usd,0),1.0) AS decimal(14,2)) AS annual_wage_usd
    FROM hr.compensation_history ch
    JOIN hr.persons p ON p.person_id = ch.person_id
    LEFT JOIN dw.dim_fx fx ON fx.currency_code = ch.currency_code AND fx.year = extract(year from ch.effective_from)::smallint
    WHERE ch.person_id <= 9
    ORDER BY ch.person_id, ch.effective_from;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_SalesPerStaff(p_year int DEFAULT NULL, p_top int DEFAULT 25)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT st.staff_key, st.full_name, st.role_title, st.home_shop_id,
           COUNT(DISTINCT f.transaction_id) AS transactions,
           SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_staff st ON st.staff_key = f.staff_key
    WHERE (p_year IS NULL OR extract(year from f.occurred_at) = p_year)
    GROUP BY st.staff_key, st.full_name, st.role_title, st.home_shop_id
    ORDER BY revenue_usd DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_WageBillByCountry(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT pr.country_code,
           CAST(SUM(pl.gross / COALESCE(NULLIF(fx.rate_to_usd,0),1.0)) AS decimal(16,2)) AS wage_bill_usd,
           COUNT(DISTINCT pl.spell_id) AS staff
    FROM hr.payroll_lines pl
    JOIN hr.payroll_runs pr ON pr.payroll_run_id = pl.payroll_run_id
    LEFT JOIN dw.dim_fx fx ON fx.currency_code = pr.currency_code AND fx.year = extract(year from pr.paid_at)::smallint
    WHERE pr.status='posted' AND (p_year IS NULL OR extract(year from pr.paid_at) = p_year)
    GROUP BY pr.country_code ORDER BY wage_bill_usd DESC;
    RETURN c;
END $$;
