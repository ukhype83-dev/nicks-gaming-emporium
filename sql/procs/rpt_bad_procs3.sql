-- everything about a customer: SELECT * across many tables
CREATE OR ALTER PROCEDURE rpt.usp_GetEverythingAboutACustomer
    @customer_id BIGINT
AS
BEGIN
    SET NOCOUNT ON;
    SELECT * FROM dbo.customers WHERE customer_id = @customer_id;
    SELECT * FROM dbo.customer_addresses WHERE customer_id = @customer_id;
    SELECT * FROM dbo.loyalty_memberships WHERE customer_id = @customer_id;
    SELECT * FROM dw.agg_customer_ltv WHERE customer_key = @customer_id;
    -- every line they ever bought, unfiltered SELECT *
    SELECT * FROM dw.fact_sales WHERE customer_key = @customer_id ORDER BY occurred_at;
END
GO

-- fuzzy product search: LOWER() + leading-wildcard on three columns
CREATE OR ALTER PROCEDURE rpt.usp_SearchProductsFuzzy
    @term NVARCHAR(100)
AS
BEGIN
    SET NOCOUNT ON;
    SELECT product_key, title, platform_name, publisher, developer
    FROM dw.dim_product
    WHERE LOWER(title)     LIKE '%' + LOWER(@term) + '%'
       OR LOWER(publisher) LIKE '%' + LOWER(@term) + '%'
       OR LOWER(developer) LIKE '%' + LOWER(@term) + '%'
    ORDER BY title;
END
GO

-- running monthly total via a cursor (a window function would do)
CREATE OR ALTER PROCEDURE rpt.usp_RunningTotalCursor
    @year SMALLINT = 2008
AS
BEGIN
    SET NOCOUNT ON;
    CREATE TABLE #r (ym CHAR(7), rev DECIMAL(18,2), running DECIMAL(18,2));
    DECLARE @ym CHAR(7), @rev DECIMAL(18,2), @run DECIMAL(18,2) = 0;
    DECLARE c CURSOR LOCAL FAST_FORWARD FOR
        SELECT year_month, net_revenue_usd FROM dw.agg_sales_by_month
        WHERE LEFT(year_month,4)=CAST(@year AS CHAR(4)) ORDER BY year_month;
    OPEN c; FETCH NEXT FROM c INTO @ym, @rev;
    WHILE @@FETCH_STATUS = 0
    BEGIN
        SET @run += @rev;
        INSERT #r VALUES (@ym, @rev, @run);
        FETCH NEXT FROM c INTO @ym, @rev;
    END
    CLOSE c; DEALLOCATE c;
    SELECT * FROM #r ORDER BY ym;
END
GO

-- separate scans in a UNION instead of one GROUP BY
CREATE OR ALTER PROCEDURE rpt.usp_YearlyComparison_ManyScans
AS
BEGIN
    SET NOCOUNT ON;
    SELECT 2006 AS yr, SUM(line_total_usd) AS rev FROM dw.fact_sales WHERE YEAR(occurred_at)=2006
    UNION ALL SELECT 2007, SUM(line_total_usd) FROM dw.fact_sales WHERE YEAR(occurred_at)=2007
    UNION ALL SELECT 2008, SUM(line_total_usd) FROM dw.fact_sales WHERE YEAR(occurred_at)=2008
    UNION ALL SELECT 2009, SUM(line_total_usd) FROM dw.fact_sales WHERE YEAR(occurred_at)=2009
    UNION ALL SELECT 2010, SUM(line_total_usd) FROM dw.fact_sales WHERE YEAR(occurred_at)=2010;
END
GO

-- customer order history: a function on the key defeats the index
CREATE OR ALTER PROCEDURE rpt.usp_CustomerOrderHistory_Slow
    @customer_id BIGINT
AS
BEGIN
    SET NOCOUNT ON;
    SELECT f.occurred_at, p.title, f.quantity, f.line_total_usd
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE CAST(f.customer_key AS VARCHAR(20)) = CAST(@customer_id AS VARCHAR(20))  -- CAST kills the seek
    ORDER BY f.occurred_at;
END
GO

-- "the numbers" dashboard: a pile of COUNT(DISTINCT) scans in one row
CREATE OR ALTER PROCEDURE rpt.usp_AllTheNumbers
AS
BEGIN
    SET NOCOUNT ON;
    SELECT
        (SELECT COUNT(DISTINCT customer_key) FROM dw.fact_sales) AS distinct_buyers,
        (SELECT COUNT(DISTINCT product_key)  FROM dw.fact_sales) AS distinct_products,
        (SELECT COUNT(DISTINCT shop_key)     FROM dw.fact_sales) AS distinct_shops,
        (SELECT COUNT(DISTINCT staff_key)    FROM dw.fact_sales) AS distinct_staff,
        (SELECT COUNT(*) FROM dw.fact_sales WHERE is_return=1)   AS total_returns;
END
GO

-- report layered on the view stack
CREATE OR ALTER PROCEDURE rpt.usp_ShopReport_vOLD_dont_delete
    @region NVARCHAR(32) = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP 5000 shop_name, region, SUM(line_total_usd) AS rev
    FROM rpt.v_sales_final
    WHERE (@region IS NULL OR region = @region)
    GROUP BY shop_name, region
    ORDER BY rev DESC;
END
GO
