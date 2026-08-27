/* =============================================================
   rpt — product / hardware catalog analytics, part 3 (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_HardwareInstallBase()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.platform_name,
           SUM(CASE WHEN NOT f.is_return THEN f.quantity ELSE 0 END) AS consoles_sold,
           SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key=f.product_key
    WHERE f.is_hardware AND p.category IN ('console','handheld','computer')
    GROUP BY p.platform_name ORDER BY consoles_sold DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_AccessorySales(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.title, p.manufacturer, SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key=f.product_key
    WHERE p.category='accessory' AND NOT f.is_return AND (p_year IS NULL OR extract(year from f.occurred_at)=p_year)
    GROUP BY p.title, p.manufacturer ORDER BY units DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ConsoleVsHandheld()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.category, SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key=f.product_key
    WHERE f.is_hardware AND NOT f.is_return GROUP BY p.category ORDER BY revenue_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TitlesPerPlatform()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT platform_name, COUNT(*) AS catalog_titles
    FROM dw.dim_product WHERE product_kind='game'
    GROUP BY platform_name ORDER BY catalog_titles DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ProductsNeverSold(p_top int DEFAULT 200)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.product_key, p.title, p.platform_name, p.publisher
    FROM dw.dim_product p
    LEFT JOIN dw.agg_product_performance pp ON pp.product_key=p.product_key
    WHERE pp.product_key IS NULL AND p.product_kind='game'
    ORDER BY p.title
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ManufacturerShare()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT COALESCE(p.manufacturer,'(unknown)') AS manufacturer,
           SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key=f.product_key
    WHERE f.is_hardware AND NOT f.is_return GROUP BY p.manufacturer ORDER BY revenue_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ReleaseCadence()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT extract(year from first_release_date) AS release_year, COUNT(*) AS titles_released
    FROM dw.dim_product WHERE product_kind='game' AND first_release_date IS NOT NULL
    GROUP BY extract(year from first_release_date) ORDER BY release_year;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_CatalogCoverage()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT COUNT(*) AS catalog_titles,
           SUM(CASE WHEN pp.product_key IS NOT NULL THEN 1 ELSE 0 END) AS ever_sold,
           CAST(100.0*SUM(CASE WHEN pp.product_key IS NOT NULL THEN 1 ELSE 0 END)/COUNT(*) AS decimal(5,1)) AS coverage_pct
    FROM dw.dim_product p
    LEFT JOIN dw.agg_product_performance pp ON pp.product_key=p.product_key
    WHERE p.product_kind='game';
    RETURN c;
END $$;
