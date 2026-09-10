-- two more scalar UDFs
CREATE OR ALTER FUNCTION dbo.fn_GetShopName (@shop_id BIGINT)
RETURNS NVARCHAR(255) AS BEGIN
    DECLARE @n NVARCHAR(255);
    SELECT @n = name FROM dbo.shops WHERE shop_id = @shop_id;   -- one lookup per row
    RETURN ISNULL(@n, N'(unknown shop)');
END
GO
CREATE OR ALTER FUNCTION dbo.fn_GetCustomerCountry (@customer_id BIGINT)
RETURNS CHAR(2) AS BEGIN
    RETURN (SELECT TOP 1 country_code FROM dbo.customer_addresses WHERE customer_id = @customer_id);
END
GO

-- inventory check: cursors over every shop, one query each
CREATE OR ALTER PROCEDURE rpt.usp_ToddsInventoryCheck
AS
BEGIN
    SET NOCOUNT ON;
    CREATE TABLE #r (shop_id BIGINT, skus INT, units BIGINT);
    DECLARE @sid BIGINT;
    DECLARE c CURSOR LOCAL FAST_FORWARD FOR SELECT shop_id FROM dbo.shops;
    OPEN c; FETCH NEXT FROM c INTO @sid;
    WHILE @@FETCH_STATUS = 0
    BEGIN
        INSERT #r SELECT @sid, COUNT(*), SUM(on_hand) FROM dbo.inventory WHERE shop_id = @sid;  -- one scan per shop
        FETCH NEXT FROM c INTO @sid;
    END
    CLOSE c; DEALLOCATE c;
    SELECT r.*, dbo.fn_GetShopName(r.shop_id) AS shop_name FROM #r r ORDER BY units DESC;  -- + UDF tax on the way out
END
GO

-- dedupe customers: functions on both sides of a self-join, bounded to one country
CREATE OR ALTER PROCEDURE rpt.usp_FindDuplicateCustomers
    @country CHAR(2) = 'IE'
AS
BEGIN
    SET NOCOUNT ON;
    SELECT a.customer_key AS cust_a, b.customer_key AS cust_b, a.city
    FROM dw.dim_customer a
    JOIN dw.dim_customer b
      ON UPPER(LTRIM(RTRIM(a.city))) = UPPER(LTRIM(RTRIM(b.city)))   -- functions kill any index
     AND a.birth_year = b.birth_year
     AND a.customer_key < b.customer_key
    WHERE a.country_code = @country AND b.country_code = @country
    ORDER BY a.city;
END
GO

-- yearly totals built one year at a time in a cursor
CREATE OR ALTER PROCEDURE rpt.usp_SalesByYear_Cursor
AS
BEGIN
    SET NOCOUNT ON;
    CREATE TABLE #y (yr INT, rev DECIMAL(18,2));
    DECLARE @yr INT = 1986;
    WHILE @yr <= 2016
    BEGIN
        INSERT #y SELECT @yr, SUM(line_total_usd) FROM dw.fact_sales WHERE YEAR(occurred_at) = @yr;  -- full-ish scan per year
        SET @yr += 1;
    END
    SELECT * FROM #y ORDER BY yr;
END
GO

-- views-on-views (reuses rpt.v_sales_final)
CREATE OR ALTER PROCEDURE rpt.usp_CustomerReport_vFinal_USE_THIS_ONE
    @customer_key BIGINT
AS
BEGIN
    SET NOCOUNT ON;
    SELECT * FROM rpt.v_sales_final WHERE customer_key = @customer_key ORDER BY occurred_at DESC;
END
GO

-- big spenders via a scalar UDF in the WHERE + non-SARGable
CREATE OR ALTER PROCEDURE rpt.usp_GetBigSpenders_Slow
    @country CHAR(2) = 'US'
AS
BEGIN
    SET NOCOUNT ON;
    SELECT c.customer_key, l.net_spend_usd, dbo.fn_GetCustomerCountry(c.customer_key) AS country_again
    FROM dw.dim_customer c
    JOIN dw.agg_customer_ltv l ON l.customer_key = c.customer_key
    WHERE dbo.fn_GetCustomerCountry(c.customer_key) = @country      -- UDF per row instead of c.country_code
      AND l.net_spend_usd > 1000
    ORDER BY l.net_spend_usd DESC;
END
GO

-- functions on the date column + OR soup
CREATE OR ALTER PROCEDURE rpt.usp_WhatSoldLastChristmas
    @year SMALLINT = 2007
AS
BEGIN
    SET NOCOUNT ON;
    SELECT p.title, p.platform_name, SUM(f.quantity) AS units
    FROM dw.fact_sales f
    JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE YEAR(f.occurred_at) = @year                              -- non-SARGable
      AND (MONTH(f.occurred_at) = 12 OR MONTH(f.occurred_at) = 11) -- OR soup, functions
    GROUP BY p.title, p.platform_name
    ORDER BY units DESC;
END
GO

-- top reviewers computed with an N+1 correlated subquery per row
CREATE OR ALTER PROCEDURE rpt.usp_TopReviewersNPlusOne
    @min_reviews INT = 50
AS
BEGIN
    SET NOCOUNT ON;
    SELECT a.account_id, a.username,
           (SELECT COUNT(*) FROM web.reviews r WHERE r.account_id = a.account_id) AS review_count,
           (SELECT AVG(CAST(r.rating AS FLOAT)) FROM web.reviews r WHERE r.account_id = a.account_id) AS avg_rating
    FROM web.accounts a
    WHERE (SELECT COUNT(*) FROM web.reviews r WHERE r.account_id = a.account_id) >= @min_reviews  -- runs the subquery for EVERY account
    ORDER BY review_count DESC;
END
GO
