CREATE OR ALTER PROCEDURE rpt.usp_rpt_GenreTrends
AS
BEGIN
    SET NOCOUNT ON;
    SELECT YEAR(f.occurred_at) AS yr, ISNULL(p.category,N'(uncat)') AS genre,
           SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE p.product_kind='game' AND f.is_return=0
    GROUP BY YEAR(f.occurred_at), p.category
    ORDER BY yr, revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_PublisherDeepDive
    @publisher NVARCHAR(300)
AS
BEGIN
    SET NOCOUNT ON;
    SELECT p.title, p.platform_name, pp.units_sold, pp.revenue_usd, pp.first_sold, pp.last_sold
    FROM dw.dim_product p
    JOIN dw.agg_product_performance pp ON pp.product_key = p.product_key
    WHERE p.publisher = @publisher
    ORDER BY pp.revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_ReleaseYearPerformance
AS
BEGIN
    SET NOCOUNT ON;
    SELECT YEAR(p.first_release_date) AS release_year,
           COUNT(DISTINCT p.product_key) AS titles,
           SUM(pp.units_sold) AS units_sold, SUM(pp.revenue_usd) AS revenue_usd
    FROM dw.dim_product p
    JOIN dw.agg_product_performance pp ON pp.product_key = p.product_key
    WHERE p.first_release_date IS NOT NULL
    GROUP BY YEAR(p.first_release_date) ORDER BY release_year;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_LongTail
AS
BEGIN
    SET NOCOUNT ON;
    -- Pareto: what share of revenue comes from the top N% of titles
    ;WITH ranked AS (
        SELECT product_key, revenue_usd,
                NTILE(100) OVER (ORDER BY revenue_usd DESC) AS pct_rank
        FROM dw.agg_product_performance
    )
    SELECT CASE WHEN pct_rank<=1 THEN N'top 1%' WHEN pct_rank<=5 THEN N'top 5%'
                WHEN pct_rank<=20 THEN N'top 20%' ELSE N'long tail (80%)' END AS band,
           COUNT(*) AS titles, SUM(revenue_usd) AS revenue_usd
    FROM ranked
    GROUP BY CASE WHEN pct_rank<=1 THEN N'top 1%' WHEN pct_rank<=5 THEN N'top 5%'
                  WHEN pct_rank<=20 THEN N'top 20%' ELSE N'long tail (80%)' END
    ORDER BY revenue_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_ReviewVsSales
    @top INT = 50
AS
BEGIN
    SET NOCOUNT ON;
    -- do better-reviewed games sell more? (join review sentiment to units)
    SELECT TOP (@top) p.title, p.platform_name,
           pp.units_sold, pp.revenue_usd,
           rv.review_count, CAST(rv.avg_rating AS DECIMAL(4,2)) AS avg_rating
    FROM dw.agg_product_performance pp
    JOIN dw.dim_product p ON p.product_key = pp.product_key
    JOIN (SELECT release_id, COUNT(*) AS review_count, AVG(CAST(rating AS FLOAT)) AS avg_rating
          FROM web.reviews WHERE release_id IS NOT NULL GROUP BY release_id) rv
      ON rv.release_id = p.product_key
    WHERE rv.review_count >= 5
    ORDER BY pp.units_sold DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_UsedVsNewByProduct
    @top INT = 30
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) p.title, p.platform_name,
           SUM(CASE WHEN f.condition_key=1 THEN f.quantity ELSE 0 END) AS new_units,
           SUM(CASE WHEN f.condition_key>1 THEN f.quantity ELSE 0 END) AS used_units
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE f.is_return=0 AND p.product_kind='game'
    GROUP BY p.title, p.platform_name
    HAVING SUM(CASE WHEN f.condition_key>1 THEN f.quantity ELSE 0 END) > 0
    ORDER BY used_units DESC;
END
GO
