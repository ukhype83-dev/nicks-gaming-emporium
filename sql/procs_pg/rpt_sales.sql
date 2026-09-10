/* =============================================================
   rpt — sales reports (PostgreSQL port; the "good" side)
   -------------------------------------------------------------
   Each report is a FUNCTION returning a refcursor over its result
   set — the faithful "call a proc, get rows" form (no per-proc
   output-column declaration). Consume within a transaction:

     BEGIN;
     SELECT rpt.usp_rpt_SalesByMonth('2016-01','2016-12');  -- returns a cursor name
     FETCH ALL FROM "<cursor>";
     COMMIT;

   (loadgen drains them the same way.) Recurring T-SQL->PG transforms:
   @p->p_p, TOP(n)->LIMIT n, COUNT_BIG->COUNT, YEAR(x)->extract(year
   from x), is_return=0 -> NOT is_return (real boolean now).
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_SalesByMonth(p_from_ym char(7) DEFAULT '1986-01', p_to_ym char(7) DEFAULT '2016-12')
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT year_month, tx_count, units, gross_revenue_usd, return_value_usd,
           net_revenue_usd, software_revenue_usd, hardware_revenue_usd, avg_line_usd
    FROM dw.agg_sales_by_month
    WHERE year_month BETWEEN p_from_ym AND p_to_ym
    ORDER BY year_month;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_SalesByPlatform(p_from_ym char(7) DEFAULT '1986-01', p_to_ym char(7) DEFAULT '2016-12')
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT platform_name,
           SUM(units) AS units, SUM(line_count) AS line_count, SUM(revenue_usd) AS revenue_usd
    FROM dw.agg_sales_by_month_platform
    WHERE year_month BETWEEN p_from_ym AND p_to_ym
    GROUP BY platform_name
    ORDER BY revenue_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TopSellers(p_top int DEFAULT 25, p_product_kind varchar(12) DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.product_key, p.product_kind, p.title, p.platform_name,
           pp.units_sold, pp.revenue_usd, pp.distinct_customers, pp.first_sold, pp.last_sold
    FROM dw.agg_product_performance pp
    JOIN dw.dim_product p ON p.product_key = pp.product_key
    WHERE (p_product_kind IS NULL OR p.product_kind = p_product_kind)
    ORDER BY pp.revenue_usd DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_SalesByShop(p_from_key int DEFAULT 19860101, p_to_key int DEFAULT 20161231, p_top int DEFAULT 50)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT s.shop_key, s.shop_code, s.name, s.country_code, s.city,
           SUM(a.tx_count) AS tx_count, SUM(a.units) AS units, SUM(a.revenue_usd) AS revenue_usd
    FROM dw.agg_sales_by_day_shop a
    JOIN dw.dim_shop s ON s.shop_key = a.shop_key
    WHERE a.date_key BETWEEN p_from_key AND p_to_key
    GROUP BY s.shop_key, s.shop_code, s.name, s.country_code, s.city
    ORDER BY revenue_usd DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_SalesByRegion(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT g.region,
           COUNT(*) AS line_count,
           SUM(CASE WHEN NOT f.is_return THEN f.quantity ELSE 0 END) AS units,
           SUM(f.line_total_usd) AS net_revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_shop s ON s.shop_key = f.shop_key
    JOIN dw.dim_geography g ON g.country_code = s.country_code
    WHERE (p_year IS NULL OR extract(year from f.occurred_at) = p_year)
    GROUP BY g.region
    ORDER BY net_revenue_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ChannelMix(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT ch.channel_name,
           COUNT(DISTINCT f.transaction_id) AS tx_count,
           SUM(f.line_total_usd) AS net_revenue_usd,
           CAST(100.0 * SUM(f.line_total_usd) / NULLIF(SUM(SUM(f.line_total_usd)) OVER (), 0) AS decimal(5,2)) AS pct
    FROM dw.fact_sales f
    JOIN dw.dim_channel ch ON ch.channel_key = f.channel_key
    WHERE (p_year IS NULL OR extract(year from f.occurred_at) = p_year)
    GROUP BY ch.channel_name
    ORDER BY net_revenue_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_NewVsUsedSplit(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT co.condition_name, co.is_used,
           COUNT(*) AS line_count,
           SUM(CASE WHEN NOT f.is_return THEN f.quantity ELSE 0 END) AS units,
           SUM(f.line_total_usd) AS net_revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_condition co ON co.condition_key = f.condition_key
    WHERE (p_year IS NULL OR extract(year from f.occurred_at) = p_year)
    GROUP BY co.condition_name, co.is_used
    ORDER BY net_revenue_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_YoYGrowth()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    WITH yearly AS (
        SELECT LEFT(year_month,4) AS yr, SUM(net_revenue_usd) AS rev
        FROM dw.agg_sales_by_month GROUP BY LEFT(year_month,4)
    )
    SELECT yr, rev,
           LAG(rev) OVER (ORDER BY yr) AS prior_rev,
           CAST(100.0*(rev - LAG(rev) OVER (ORDER BY yr)) / NULLIF(LAG(rev) OVER (ORDER BY yr),0) AS decimal(6,1)) AS yoy_pct
    FROM yearly ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_SeasonalPattern()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT RIGHT(year_month,2) AS month_num,
           SUM(net_revenue_usd) AS total_net_usd,
           CAST(AVG(net_revenue_usd) AS decimal(16,2)) AS avg_month_net_usd
    FROM dw.agg_sales_by_month
    GROUP BY RIGHT(year_month,2) ORDER BY month_num;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_DailySalesForShop(p_shop_key bigint, p_from_key int DEFAULT 19860101, p_to_key int DEFAULT 20161231)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT a.date_key, d.full_date, a.tx_count, a.units, a.revenue_usd
    FROM dw.agg_sales_by_day_shop a
    JOIN dw.dim_date d ON d.date_key = a.date_key
    WHERE a.shop_key = p_shop_key AND a.date_key BETWEEN p_from_key AND p_to_key
    ORDER BY a.date_key;
    RETURN c;
END $$;
