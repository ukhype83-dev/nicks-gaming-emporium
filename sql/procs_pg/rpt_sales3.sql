/* =============================================================
   rpt — sales analytics, part 3 (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_SalesByQuarter()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT d.year, d.quarter, SUM(f.line_total_usd) AS revenue_usd, SUM(f.quantity) AS units
    FROM dw.fact_sales f JOIN dw.dim_date d ON d.date_key=f.date_key
    WHERE NOT f.is_return GROUP BY d.year, d.quarter ORDER BY d.year, d.quarter;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_SalesByEra()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT d.era, SUM(f.line_total_usd) AS revenue_usd, SUM(f.quantity) AS units, COUNT(*) AS lines
    FROM dw.fact_sales f JOIN dw.dim_date d ON d.date_key=f.date_key
    GROUP BY d.era ORDER BY MIN(d.date_key);
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_MonthOverMonth(p_from_ym char(7) DEFAULT '2007-01', p_to_ym char(7) DEFAULT '2009-12')
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT year_month, net_revenue_usd,
           LAG(net_revenue_usd) OVER (ORDER BY year_month) AS prev_month,
           CAST(100.0*(net_revenue_usd-LAG(net_revenue_usd) OVER (ORDER BY year_month))
                /NULLIF(LAG(net_revenue_usd) OVER (ORDER BY year_month),0) AS decimal(6,1)) AS mom_pct
    FROM dw.agg_sales_by_month WHERE year_month BETWEEN p_from_ym AND p_to_ym ORDER BY year_month;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_HolidaySeasonSales()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT d.year, d.is_holiday_season, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_date d ON d.date_key=f.date_key
    WHERE NOT f.is_return GROUP BY d.year, d.is_holiday_season ORDER BY d.year, d.is_holiday_season;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_WeekendVsWeekday(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT d.is_weekend, COUNT(*) AS lines, SUM(f.line_total_usd) AS revenue_usd,
           CAST(AVG(f.line_total_usd) AS decimal(12,2)) AS avg_line_usd
    FROM dw.fact_sales f JOIN dw.dim_date d ON d.date_key=f.date_key
    WHERE (p_year IS NULL OR d.year=p_year) GROUP BY d.is_weekend;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_UnitsByYear()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT extract(year from occurred_at) AS yr,
           SUM(CASE WHEN NOT is_hardware THEN quantity ELSE 0 END) AS software_units,
           SUM(CASE WHEN is_hardware THEN quantity ELSE 0 END) AS hardware_units
    FROM dw.fact_sales WHERE NOT is_return GROUP BY extract(year from occurred_at) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TopShopsAllTime(p_top int DEFAULT 50)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT s.name, s.country_code, s.city, SUM(a.revenue_usd) AS lifetime_usd
    FROM dw.agg_sales_by_day_shop a JOIN dw.dim_shop s ON s.shop_key=a.shop_key
    GROUP BY s.name, s.country_code, s.city ORDER BY lifetime_usd DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_RevenuePerShopByYear()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT LEFT(a.date_key::text,4) AS yr,
           COUNT(DISTINCT a.shop_key) AS shops,
           SUM(a.revenue_usd) AS revenue_usd,
           CAST(SUM(a.revenue_usd)/NULLIF(COUNT(DISTINCT a.shop_key),0) AS decimal(14,2)) AS rev_per_shop
    FROM dw.agg_sales_by_day_shop a
    GROUP BY LEFT(a.date_key::text,4) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_GenreByYear(p_year int DEFAULT 2008)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT COALESCE(p.category,'(uncat)') AS genre, SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key=f.product_key
    WHERE p.product_kind='game' AND extract(year from f.occurred_at)=p_year AND NOT f.is_return
    GROUP BY p.category ORDER BY revenue_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_AvgBasketByYear()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT LEFT(year_month,4) AS yr,
           SUM(tx_count) AS transactions,
           CAST(SUM(net_revenue_usd)/NULLIF(SUM(tx_count),0) AS decimal(12,2)) AS avg_basket_usd
    FROM dw.agg_sales_by_month GROUP BY LEFT(year_month,4) ORDER BY yr;
    RETURN c;
END $$;
