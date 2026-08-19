CREATE OR ALTER PROCEDURE rpt.usp_rpt_HardwareInstallBase AS
BEGIN SET NOCOUNT ON;
    SELECT p.platform_name,
           SUM(CASE WHEN f.is_return=0 THEN f.quantity ELSE 0 END) AS consoles_sold,
           SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key=f.product_key
    WHERE f.is_hardware=1 AND p.category IN ('console','handheld','computer')
    GROUP BY p.platform_name ORDER BY consoles_sold DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_AccessorySales @year SMALLINT=NULL AS
BEGIN SET NOCOUNT ON;
    SELECT p.title, p.manufacturer, SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key=f.product_key
    WHERE p.category='accessory' AND f.is_return=0 AND (@year IS NULL OR YEAR(f.occurred_at)=@year)
    GROUP BY p.title, p.manufacturer ORDER BY units DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_ConsoleVsHandheld AS
BEGIN SET NOCOUNT ON;
    SELECT p.category, SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key=f.product_key
    WHERE f.is_hardware=1 AND f.is_return=0 GROUP BY p.category ORDER BY revenue_usd DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_TitlesPerPlatform AS
BEGIN SET NOCOUNT ON;
    SELECT platform_name, COUNT(*) AS catalog_titles
    FROM dw.dim_product WHERE product_kind='game'
    GROUP BY platform_name ORDER BY catalog_titles DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_ProductsNeverSold @top INT=200 AS
BEGIN SET NOCOUNT ON;
    SELECT TOP (@top) p.product_key, p.title, p.platform_name, p.publisher
    FROM dw.dim_product p
    LEFT JOIN dw.agg_product_performance pp ON pp.product_key=p.product_key
    WHERE pp.product_key IS NULL AND p.product_kind='game'
    ORDER BY p.title;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_ManufacturerShare AS
BEGIN SET NOCOUNT ON;
    SELECT ISNULL(p.manufacturer,N'(unknown)') AS manufacturer,
           SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key=f.product_key
    WHERE f.is_hardware=1 AND f.is_return=0 GROUP BY p.manufacturer ORDER BY revenue_usd DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_ReleaseCadence AS
BEGIN SET NOCOUNT ON;
    SELECT YEAR(first_release_date) AS release_year, COUNT(*) AS titles_released
    FROM dw.dim_product WHERE product_kind='game' AND first_release_date IS NOT NULL
    GROUP BY YEAR(first_release_date) ORDER BY release_year;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_CatalogCoverage AS
BEGIN SET NOCOUNT ON;
    SELECT COUNT(*) AS catalog_titles,
           SUM(CASE WHEN pp.product_key IS NOT NULL THEN 1 ELSE 0 END) AS ever_sold,
           CAST(100.0*SUM(CASE WHEN pp.product_key IS NOT NULL THEN 1 ELSE 0 END)/COUNT(*) AS DECIMAL(5,1)) AS coverage_pct
    FROM dw.dim_product p
    LEFT JOIN dw.agg_product_performance pp ON pp.product_key=p.product_key
    WHERE p.product_kind='game';
END
GO
