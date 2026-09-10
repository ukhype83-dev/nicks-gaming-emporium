/* =============================================================
   rpt — product / catalog reports (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ProductDetail(p_product_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.product_key, p.product_kind, p.title, p.platform_name, p.category,
           p.publisher, p.developer, p.first_release_date,
           pp.units_sold, pp.revenue_usd, pp.return_count, pp.distinct_customers,
           pp.first_sold, pp.last_sold
    FROM dw.dim_product p
    LEFT JOIN dw.agg_product_performance pp ON pp.product_key = p.product_key
    WHERE p.product_key = p_product_key;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_SlowMovers(p_max_units bigint DEFAULT 5, p_product_kind varchar(12) DEFAULT 'game')
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.product_key, p.title, p.platform_name, pp.units_sold, pp.revenue_usd, pp.last_sold
    FROM dw.agg_product_performance pp
    JOIN dw.dim_product p ON p.product_key = pp.product_key
    WHERE pp.units_sold <= p_max_units AND (p_product_kind IS NULL OR p.product_kind = p_product_kind)
    ORDER BY pp.units_sold, pp.revenue_usd;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_GenreBreakdown(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT COALESCE(p.category, '(uncategorised)') AS genre,
           COUNT(*) AS line_count,
           SUM(CASE WHEN NOT f.is_return THEN f.quantity ELSE 0 END) AS units,
           SUM(f.line_total_usd) AS net_revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE p.product_kind = 'game' AND (p_year IS NULL OR extract(year from f.occurred_at) = p_year)
    GROUP BY p.category ORDER BY net_revenue_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_PublisherLeaderboard(p_top int DEFAULT 25, p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT COALESCE(p.publisher,'(unknown)') AS publisher,
           COUNT(DISTINCT p.product_key) AS titles,
           SUM(CASE WHEN NOT f.is_return THEN f.quantity ELSE 0 END) AS units,
           SUM(f.line_total_usd) AS net_revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE (p_year IS NULL OR extract(year from f.occurred_at) = p_year)
    GROUP BY p.publisher ORDER BY net_revenue_usd DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_PlatformLifecycle(p_platform_name varchar(100))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT year_month, SUM(units) AS units, SUM(revenue_usd) AS revenue_usd
    FROM dw.agg_sales_by_month_platform
    WHERE platform_name = p_platform_name
    GROUP BY year_month ORDER BY year_month;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TradeInVelocity(p_top int DEFAULT 25)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.product_key, p.title, p.platform_name,
           COUNT(*) AS trade_ins, SUM(t.offer_amount_usd) AS total_offered_usd
    FROM dw.fact_tradein t
    JOIN dw.dim_product p ON p.product_key = t.product_key
    GROUP BY p.product_key, p.title, p.platform_name
    ORDER BY trade_ins DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_HardwareAttachRate(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    -- software units vs hardware units by platform (attach = games per console)
    SELECT pl.platform_name,
           SUM(CASE WHEN f.is_hardware AND NOT f.is_return THEN f.quantity ELSE 0 END) AS hardware_units,
           SUM(CASE WHEN NOT f.is_hardware AND NOT f.is_return THEN f.quantity ELSE 0 END) AS software_units,
           CAST(1.0*SUM(CASE WHEN NOT f.is_hardware AND NOT f.is_return THEN f.quantity ELSE 0 END)
                / NULLIF(SUM(CASE WHEN f.is_hardware AND NOT f.is_return THEN f.quantity ELSE 0 END),0)
                AS decimal(8,2)) AS attach_rate
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    JOIN dw.dim_platform pl ON pl.platform_key = p.platform_key
    WHERE (p_year IS NULL OR extract(year from f.occurred_at) = p_year)
    GROUP BY pl.platform_name
    HAVING SUM(CASE WHEN f.is_hardware THEN 1 ELSE 0 END) > 0
    ORDER BY attach_rate DESC;
    RETURN c;
END $$;
