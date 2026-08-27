/* =============================================================
   rpt — finance reports (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_PnL(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT year_month, revenue_usd, cogs_usd,
           revenue_usd - cogs_usd AS gross_profit_usd,
           wages_usd, severance_usd, rent_usd, other_opex_usd,
           net_income_usd, shops_active, staff_active
    FROM finance.monthly_summary
    WHERE (p_year IS NULL OR LEFT(year_month,4) = p_year::text)
    ORDER BY year_month;
    RETURN c;
END $$;

/* The death-spiral: wages as a % of revenue, climbing to >100% by 2016. */
CREATE OR REPLACE FUNCTION rpt.usp_rpt_WagesPctRevenue()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT LEFT(year_month,4) AS yr,
           SUM(revenue_usd) AS revenue_usd,
           SUM(wages_usd)   AS wages_usd,
           CAST(100.0 * SUM(wages_usd) / NULLIF(SUM(revenue_usd),0) AS decimal(6,1)) AS wages_pct_revenue,
           SUM(staff_active)/NULLIF(COUNT(*),0) AS avg_staff_active
    FROM finance.monthly_summary
    GROUP BY LEFT(year_month,4)
    ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_MarginBySegment(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT LEFT(year_month,4) AS yr,
           SUM(software_revenue_usd) AS software_rev_usd,
           SUM(hardware_revenue_usd) AS hardware_rev_usd,
           CAST(100.0*SUM(hardware_revenue_usd)/NULLIF(SUM(net_revenue_usd),0) AS decimal(5,1)) AS hardware_pct
    FROM dw.agg_sales_by_month
    WHERE (p_year IS NULL OR LEFT(year_month,4)=p_year::text)
    GROUP BY LEFT(year_month,4) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_RefundRate(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT LEFT(year_month,4) AS yr,
           SUM(gross_revenue_usd) AS gross_usd,
           SUM(-return_value_usd) AS refunds_usd,
           CAST(100.0*SUM(-return_value_usd)/NULLIF(SUM(gross_revenue_usd),0) AS decimal(5,2)) AS refund_pct
    FROM dw.agg_sales_by_month
    WHERE (p_year IS NULL OR LEFT(year_month,4)=p_year::text)
    GROUP BY LEFT(year_month,4) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_RevenueByCurrency(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT f.currency_code,
           COUNT(*) AS line_count,
           SUM(f.line_total)     AS revenue_local,
           SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    WHERE (p_year IS NULL OR extract(year from f.occurred_at) = p_year)
    GROUP BY f.currency_code ORDER BY revenue_usd DESC;
    RETURN c;
END $$;

/* Reconcile the warehouse vs the finance close (should track closely). */
CREATE OR REPLACE FUNCTION rpt.usp_rpt_WarehouseVsFinance()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT ms.year_month,
           ms.revenue_usd            AS finance_revenue_usd,
           dw.net_revenue_usd        AS warehouse_net_usd,
           ms.revenue_usd - dw.net_revenue_usd AS variance_usd
    FROM finance.monthly_summary ms
    LEFT JOIN dw.agg_sales_by_month dw ON dw.year_month = ms.year_month
    ORDER BY ms.year_month;
    RETURN c;
END $$;
