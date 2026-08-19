-- scalar UDF called once per row
CREATE OR ALTER FUNCTION dbo.fn_GetPlatformName (@platform_id INT)
RETURNS NVARCHAR(100)
AS
BEGIN
    DECLARE @n NVARCHAR(100);
    SELECT @n = name FROM dbo.platforms WHERE platform_id = @platform_id;
    RETURN ISNULL(@n, N'Unknown Platform');
END
GO

-- anti-patterns: SELECT *, scalar UDF per row, non-SARGable YEAR(), ORDER BY a computed expr
CREATE OR ALTER PROCEDURE rpt.usp_ToddsSalesReport
    @year INT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT *,
           dbo.fn_GetPlatformName(p.platform_key) AS PlatformNameAgain,   -- UDF tax
           f.line_total * 1.0 AS RevenueMaybe
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE YEAR(f.occurred_at) = ISNULL(@year, YEAR(f.occurred_at))        -- kills the index
    ORDER BY dbo.fn_GetPlatformName(p.platform_key), f.line_total DESC;    -- UDF in ORDER BY too
END
GO

-- one SUM, whole-table scan, implicit conversion (date vs varchar literal)
CREATE OR ALTER PROCEDURE rpt.usp_MonthlyNumberForBoss
    @month_str VARCHAR(7) = '2010-12'
AS
BEGIN
    SET NOCOUNT ON;
    -- convert every row's date to a string and compare
    SELECT SUM(line_total_usd) AS TheNumber
    FROM dw.fact_sales
    WHERE CONVERT(VARCHAR(7), occurred_at, 126) = @month_str;   -- non-SARGable + implicit-ish
END
GO

-- leading-wildcard LIKE with UPPER() on the column: guaranteed scan
CREATE OR ALTER PROCEDURE rpt.usp_FindGamesLikeThis
    @term NVARCHAR(100)
AS
BEGIN
    SET NOCOUNT ON;
    SELECT product_key, title, platform_name, category, publisher
    FROM dw.dim_product
    WHERE UPPER(title) LIKE '%' + UPPER(@term) + '%'      -- function on column + leading %
       OR UPPER(publisher) LIKE '%' + UPPER(@term) + '%'
    ORDER BY title;
END
GO

-- every optional filter OR'd together, one cached plan: parameter-sniffing classic
CREATE OR ALTER PROCEDURE rpt.usp_CustomerSearch
    @country CHAR(2) = NULL, @loyalty NVARCHAR(16) = NULL,
    @city NVARCHAR(200) = NULL, @signup_year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT customer_key, status, signup_date, country_code, city, loyalty_tier, age_band
    FROM dw.dim_customer
    WHERE (@country IS NULL OR country_code = @country)
      AND (@loyalty IS NULL OR loyalty_tier = @loyalty)
      AND (@city    IS NULL OR city = @city)
      AND (@signup_year IS NULL OR signup_year = @signup_year)
    ORDER BY signup_date DESC;
END
GO

-- views on views on views: each layer adds another join
CREATE OR ALTER VIEW rpt.v_sales_base AS
    SELECT f.transaction_line_id, f.occurred_at, f.product_key, f.customer_key,
           f.shop_key, f.line_total_usd, f.is_return
    FROM dw.fact_sales f;
GO
CREATE OR ALTER VIEW rpt.v_sales_enriched AS
    SELECT b.*, p.title, p.platform_name, p.category, p.publisher
    FROM rpt.v_sales_base b
    LEFT JOIN dw.dim_product p ON p.product_key = b.product_key;
GO
CREATE OR ALTER VIEW rpt.v_sales_final AS
    SELECT e.*, c.country_code, c.loyalty_tier, s.name AS shop_name, s.region
    FROM rpt.v_sales_enriched e
    LEFT JOIN dw.dim_customer c ON c.customer_key = e.customer_key
    LEFT JOIN dw.dim_shop s ON s.shop_key = e.shop_key;
GO
CREATE OR ALTER PROCEDURE rpt.usp_SalesReportV2_FINAL_v3
    @publisher NVARCHAR(300) = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP 1000 *
    FROM rpt.v_sales_final
    WHERE (@publisher IS NULL OR publisher = @publisher)
    ORDER BY occurred_at DESC;
END
GO

-- till reconciliation; till_id is NVARCHAR but an int is passed: implicit conversion, scan
CREATE OR ALTER PROCEDURE rpt.usp_TillReport
    @till INT = 1                              -- till_id is a STRING in the schema...
AS
BEGIN
    SET NOCOUNT ON;
    SELECT t.till_id, COUNT(*) AS txns, SUM(t.total) AS total_local
    FROM dbo.transactions t
    WHERE t.till_id = @till                     -- NVARCHAR = INT → implicit conversion on every row
    GROUP BY t.till_id;
END
GO

-- DISTINCT to hide a fan-out + a correlated subquery per returned row
CREATE OR ALTER PROCEDURE rpt.usp_WhoBoughtWhat
    @product_key BIGINT
AS
BEGIN
    SET NOCOUNT ON;
    SELECT DISTINCT c.customer_key, c.country_code,
           (SELECT COUNT(*) FROM dw.fact_sales f2
            WHERE f2.customer_key = c.customer_key) AS total_lines_ever   -- N+1 per row
    FROM dw.fact_sales f
    JOIN dw.dim_customer c ON c.customer_key = f.customer_key
    WHERE f.product_key = @product_key;
END
GO

-- top customers, built one row at a time in a cursor
CREATE OR ALTER PROCEDURE rpt.usp_GetTopCustomersSLOW
    @top INT = 20
AS
BEGIN
    SET NOCOUNT ON;
    CREATE TABLE #t (customer_key BIGINT, spend DECIMAL(18,2));
    DECLARE @ck BIGINT;
    DECLARE cur CURSOR LOCAL FAST_FORWARD FOR
        SELECT DISTINCT customer_key FROM dw.fact_sales WHERE customer_key IS NOT NULL;
    OPEN cur; FETCH NEXT FROM cur INTO @ck;
    WHILE @@FETCH_STATUS = 0
    BEGIN
        INSERT #t SELECT @ck, SUM(line_total_usd) FROM dw.fact_sales WHERE customer_key = @ck;  -- one scan per customer
        FETCH NEXT FROM cur INTO @ck;
    END
    CLOSE cur; DEALLOCATE cur;
    SELECT TOP (@top) * FROM #t ORDER BY spend DESC;
END
GO
