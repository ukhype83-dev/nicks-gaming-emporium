-- dumps the entire wide OBT, no filter
CREATE OR ALTER PROCEDURE rpt.usp_ReportEverything
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP 50000 * FROM dw.sales_wide ORDER BY occurred_at DESC;
END
GO

-- NOT IN / NULL trap: release_id is nullable, so any NULL yields an empty result
CREATE OR ALTER PROCEDURE rpt.usp_FindProductsNeverReviewed
AS
BEGIN
    SET NOCOUNT ON;
    SELECT product_key, title, platform_name
    FROM dw.dim_product
    WHERE product_kind = 'game'
      AND product_key NOT IN (SELECT release_id FROM web.reviews);   -- NULLs → empty result
END
GO

-- top sellers, platform via the UDF tax instead of the join column
CREATE OR ALTER PROCEDURE rpt.usp_TopSellersWithUDF
    @top INT = 50
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) p.title, dbo.fn_GetPlatformName(p.platform_key) AS platform,
           pp.units_sold, pp.revenue_usd
    FROM dw.agg_product_performance pp
    JOIN dw.dim_product p ON p.product_key = pp.product_key
    ORDER BY pp.units_sold DESC;
END
GO

-- buyers per year, counted one year at a time in a WHILE loop
CREATE OR ALTER PROCEDURE rpt.usp_CountBuyersByYear_Cursor
AS
BEGIN
    SET NOCOUNT ON;
    CREATE TABLE #b (yr INT, buyers INT);
    DECLARE @y INT = 1996;
    WHILE @y <= 2016
    BEGIN
        INSERT #b SELECT @y, COUNT(DISTINCT customer_key) FROM dw.fact_sales WHERE YEAR(occurred_at)=@y;
        SET @y += 1;
    END
    SELECT * FROM #b ORDER BY yr;
END
GO

-- search by anything: OR across many columns, functions everywhere
CREATE OR ALTER PROCEDURE rpt.usp_SearchByAnything
    @term NVARCHAR(100)
AS
BEGIN
    SET NOCOUNT ON;
    SELECT product_key, title, platform_name, publisher, developer, category
    FROM dw.dim_product
    WHERE LOWER(title)         LIKE '%'+LOWER(@term)+'%'
       OR LOWER(publisher)     LIKE '%'+LOWER(@term)+'%'
       OR LOWER(developer)     LIKE '%'+LOWER(@term)+'%'
       OR LOWER(category)      LIKE '%'+LOWER(@term)+'%'
       OR LOWER(platform_name) LIKE '%'+LOWER(@term)+'%'
       OR CAST(product_key AS VARCHAR(20)) = @term;
END
GO

-- builds a monthly sales email by string-concatenating in a loop
CREATE OR ALTER PROCEDURE rpt.usp_MonthlySalesEmail_StringBuilder
    @year SMALLINT = 2008
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @body NVARCHAR(MAX) = N'Sales report for ' + CAST(@year AS NVARCHAR(4)) + CHAR(10);
    DECLARE @ym CHAR(7), @rev DECIMAL(18,2);
    DECLARE c CURSOR LOCAL FAST_FORWARD FOR
        SELECT year_month, net_revenue_usd FROM dw.agg_sales_by_month
        WHERE LEFT(year_month,4)=CAST(@year AS CHAR(4)) ORDER BY year_month;
    OPEN c; FETCH NEXT FROM c INTO @ym, @rev;
    WHILE @@FETCH_STATUS = 0
    BEGIN
        SET @body += @ym + N': $' + CAST(CAST(@rev AS BIGINT) AS NVARCHAR(20)) + CHAR(10);
        FETCH NEXT FROM c INTO @ym, @rev;
    END
    CLOSE c; DEALLOCATE c;
    SELECT @body AS email_body;
END
GO
