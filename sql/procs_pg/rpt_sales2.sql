/* =============================================================
   rpt — sales analytics, part 2 (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_SalesByWeekday(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT d.day_of_week, d.day_name,
           COUNT(*) AS lines, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_date d ON d.date_key = f.date_key
    WHERE (p_year IS NULL OR d.year = p_year)
    GROUP BY d.day_of_week, d.day_name ORDER BY d.day_of_week;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_BasketAnalysis(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    WITH baskets AS (
        SELECT transaction_id, COUNT(*) AS lines, SUM(line_total_usd) AS basket_usd
        FROM dw.fact_sales
        WHERE (p_year IS NULL OR extract(year from occurred_at) = p_year) AND NOT is_return
        GROUP BY transaction_id
    )
    SELECT CASE WHEN lines=1 THEN '1' WHEN lines=2 THEN '2' WHEN lines<=4 THEN '3-4'
                WHEN lines<=8 THEN '5-8' ELSE '9+' END AS basket_size,
           COUNT(*) AS baskets, CAST(AVG(basket_usd) AS decimal(12,2)) AS avg_basket_usd
    FROM baskets
    GROUP BY CASE WHEN lines=1 THEN '1' WHEN lines=2 THEN '2' WHEN lines<=4 THEN '3-4'
                  WHEN lines<=8 THEN '5-8' ELSE '9+' END
    ORDER BY MIN(lines);
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_DiscountAnalysis(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT extract(year from occurred_at) AS yr,
           COUNT(*) AS lines,
           SUM(CASE WHEN line_discount > 0 THEN 1 ELSE 0 END) AS discounted_lines,
           CAST(100.0*SUM(CASE WHEN line_discount>0 THEN 1 ELSE 0 END)/COUNT(*) AS decimal(5,1)) AS discounted_pct,
           CAST(SUM(line_discount)/COALESCE(NULLIF(SUM(line_total+line_discount),0),1) *100 AS decimal(5,1)) AS avg_discount_depth_pct
    FROM dw.fact_sales
    WHERE NOT is_return AND (p_year IS NULL OR extract(year from occurred_at) = p_year)
    GROUP BY extract(year from occurred_at) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_PriceBands(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT CASE WHEN unit_price < 10 THEN '<$10' WHEN unit_price < 25 THEN '$10-25'
                WHEN unit_price < 50 THEN '$25-50' WHEN unit_price < 100 THEN '$50-100'
                ELSE '$100+' END AS price_band,
           COUNT(*) AS lines, SUM(quantity) AS units, SUM(line_total_usd) AS revenue_usd
    FROM dw.fact_sales
    WHERE NOT is_return AND NOT is_hardware AND (p_year IS NULL OR extract(year from occurred_at) = p_year)
    GROUP BY CASE WHEN unit_price < 10 THEN '<$10' WHEN unit_price < 25 THEN '$10-25'
                  WHEN unit_price < 50 THEN '$25-50' WHEN unit_price < 100 THEN '$50-100'
                  ELSE '$100+' END
    ORDER BY MIN(unit_price);
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_HardwareVsSoftwareTrend()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT LEFT(year_month,4) AS yr,
           SUM(software_revenue_usd) AS software_usd,
           SUM(hardware_revenue_usd) AS hardware_usd,
           CAST(100.0*SUM(hardware_revenue_usd)/NULLIF(SUM(net_revenue_usd),0) AS decimal(5,1)) AS hardware_pct
    FROM dw.agg_sales_by_month GROUP BY LEFT(year_month,4) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_FireSaleImpact()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    -- the 2016 wind-down: sales during the fire-sale window vs before
    SELECT d.is_fire_sale, d.era,
           COUNT(*) AS lines, SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd,
           CAST(AVG(f.line_discount) AS decimal(10,2)) AS avg_discount
    FROM dw.fact_sales f
    JOIN dw.dim_date d ON d.date_key = f.date_key
    WHERE d.year = 2016 AND NOT f.is_return
    GROUP BY d.is_fire_sale, d.era ORDER BY d.is_fire_sale;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ChannelGrowth()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT extract(year from f.occurred_at) AS yr, ch.channel_name,
           SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_channel ch ON ch.channel_key = f.channel_key
    WHERE NOT f.is_return
    GROUP BY extract(year from f.occurred_at), ch.channel_name
    ORDER BY yr, revenue_usd DESC;
    RETURN c;
END $$;
