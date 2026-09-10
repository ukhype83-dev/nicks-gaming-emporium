/* =============================================================
   rpt — executive dashboards (PostgreSQL port)
   ============================================================= */

/* Company health over its whole life — the death-spiral on one page. */
CREATE OR REPLACE FUNCTION rpt.usp_rpt_CompanyHealth()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT LEFT(year_month,4) AS yr,
           CAST(SUM(revenue_usd)/1e6 AS decimal(12,1)) AS revenue_musd,
           CAST(SUM(revenue_usd - cogs_usd)/1e6 AS decimal(12,1)) AS gross_profit_musd,
           CAST(SUM(net_income_usd)/1e6 AS decimal(12,1)) AS net_income_musd,
           CAST(100.0*SUM(wages_usd)/NULLIF(SUM(revenue_usd),0) AS decimal(6,1)) AS wages_pct_rev,
           MAX(shops_active) AS shops, MAX(staff_active) AS staff
    FROM finance.monthly_summary
    GROUP BY LEFT(year_month,4) ORDER BY yr;
    RETURN c;
END $$;

/* One year on a page: revenue, margin, mix, headcount, top platform. */
CREATE OR REPLACE FUNCTION rpt.usp_rpt_ExecutiveDashboard(p_year int DEFAULT 2008)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT
        p_year AS year,
        (SELECT CAST(SUM(revenue_usd)/1e6 AS decimal(12,1)) FROM finance.monthly_summary WHERE LEFT(year_month,4)=p_year::text) AS revenue_musd,
        (SELECT CAST(SUM(net_income_usd)/1e6 AS decimal(12,1)) FROM finance.monthly_summary WHERE LEFT(year_month,4)=p_year::text) AS net_income_musd,
        (SELECT CAST(100.0*SUM(wages_usd)/NULLIF(SUM(revenue_usd),0) AS decimal(6,1)) FROM finance.monthly_summary WHERE LEFT(year_month,4)=p_year::text) AS wages_pct,
        (SELECT CAST(100.0*SUM(hardware_revenue_usd)/NULLIF(SUM(net_revenue_usd),0) AS decimal(5,1)) FROM dw.agg_sales_by_month WHERE LEFT(year_month,4)=p_year::text) AS hardware_pct,
        (SELECT COUNT(*) FROM dw.dim_shop WHERE opened_date <= make_date(p_year,12,31) AND (closed_date IS NULL OR closed_date >= make_date(p_year,1,1))) AS shops_active,
        (SELECT platform_name FROM dw.agg_sales_by_month_platform WHERE LEFT(year_month,4)=p_year::text GROUP BY platform_name ORDER BY SUM(revenue_usd) DESC LIMIT 1) AS top_platform;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_KPISnapshot(p_year_month char(7) DEFAULT '2008-12')
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT ms.year_month, ms.revenue_usd, ms.cogs_usd, ms.net_income_usd,
           ms.shops_active, ms.staff_active,
           dw.units, dw.tx_count, dw.return_count, dw.avg_line_usd
    FROM finance.monthly_summary ms
    LEFT JOIN dw.agg_sales_by_month dw ON dw.year_month = ms.year_month
    WHERE ms.year_month = p_year_month;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_StoreScorecard(p_shop_key bigint, p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT s.shop_key, s.shop_code, s.name, s.country_code, s.city, s.is_open,
           SUM(a.tx_count) AS transactions, SUM(a.units) AS units,
           SUM(a.revenue_usd) AS revenue_usd,
           CAST(SUM(a.revenue_usd)/NULLIF(SUM(a.tx_count),0) AS decimal(12,2)) AS avg_basket_usd
    FROM dw.dim_shop s
    LEFT JOIN dw.agg_sales_by_day_shop a ON a.shop_key = s.shop_key
        AND (p_year IS NULL OR a.date_key BETWEEN p_year*10000 AND p_year*10000+1231)
    WHERE s.shop_key = p_shop_key
    GROUP BY s.shop_key, s.shop_code, s.name, s.country_code, s.city, s.is_open;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TopAndBottomShops(p_year int DEFAULT 2008, p_n int DEFAULT 10)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    WITH shop_rev AS (
        SELECT s.shop_key, s.name, s.country_code, SUM(a.revenue_usd) AS rev
        FROM dw.agg_sales_by_day_shop a
        JOIN dw.dim_shop s ON s.shop_key = a.shop_key
        WHERE a.date_key BETWEEN p_year*10000 AND p_year*10000+1231
        GROUP BY s.shop_key, s.name, s.country_code
    )
    SELECT 'TOP' AS band, name, country_code, rev FROM (SELECT * FROM shop_rev ORDER BY rev DESC LIMIT p_n) t
    UNION ALL
    SELECT 'BOTTOM', name, country_code, rev FROM (SELECT * FROM shop_rev ORDER BY rev ASC LIMIT p_n) b
    ORDER BY band, rev DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_MonthlyPnLTrend(p_from_ym char(7) DEFAULT '2010-01', p_to_ym char(7) DEFAULT '2016-09')
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT year_month, revenue_usd, cogs_usd, wages_usd, rent_usd, other_opex_usd,
           net_income_usd,
           CAST(100.0*(revenue_usd-cogs_usd)/NULLIF(revenue_usd,0) AS decimal(5,1)) AS gross_margin_pct
    FROM finance.monthly_summary
    WHERE year_month BETWEEN p_from_ym AND p_to_ym
    ORDER BY year_month;
    RETURN c;
END $$;
