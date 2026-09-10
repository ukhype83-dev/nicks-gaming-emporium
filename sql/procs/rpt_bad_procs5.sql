-- nested cursors: RBAR squared, genuinely slow
CREATE OR ALTER PROCEDURE rpt.usp_MonthlyBreakdown_CursorInCursor
    @year SMALLINT = 2009
AS
BEGIN
    SET NOCOUNT ON;
    CREATE TABLE #o (yr INT, mo INT, shop_key BIGINT, rev DECIMAL(18,2));
    DECLARE @mo INT, @sk BIGINT;
    DECLARE mc CURSOR LOCAL FAST_FORWARD FOR SELECT DISTINCT MONTH(occurred_at) FROM dw.fact_sales WHERE YEAR(occurred_at)=@year;
    OPEN mc; FETCH NEXT FROM mc INTO @mo;
    WHILE @@FETCH_STATUS=0
    BEGIN
        DECLARE sc CURSOR LOCAL FAST_FORWARD FOR SELECT TOP 20 shop_key FROM dw.dim_shop WHERE is_flagship=1 OR shop_key<=200;
        OPEN sc; FETCH NEXT FROM sc INTO @sk;
        WHILE @@FETCH_STATUS=0
        BEGIN
            INSERT #o SELECT @year,@mo,@sk,SUM(line_total_usd) FROM dw.fact_sales
                WHERE YEAR(occurred_at)=@year AND MONTH(occurred_at)=@mo AND shop_key=@sk;  -- scan per (month,shop)
            FETCH NEXT FROM sc INTO @sk;
        END
        CLOSE sc; DEALLOCATE sc;
        FETCH NEXT FROM mc INTO @mo;
    END
    CLOSE mc; DEALLOCATE mc;
    SELECT * FROM #o ORDER BY mo, rev DESC;
END
GO

-- non-SARGable review search over the big text column
CREATE OR ALTER PROCEDURE rpt.usp_ReviewSearch_NonSargable
    @term NVARCHAR(100)
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP 500 review_id, account_id, rating, LEFT(body,120) AS snippet
    FROM web.reviews WHERE body LIKE '%' + @term + '%'   -- leading wildcard on 12M+ text rows
    ORDER BY helpful_count DESC;
END
GO

-- one full scan per table, sequentially
CREATE OR ALTER PROCEDURE rpt.usp_CountEverything_ManyScans
AS
BEGIN
    SET NOCOUNT ON;
    SELECT 'customers' AS entity, COUNT_BIG(*) AS n FROM dbo.customers WHERE status='active'
    UNION ALL SELECT 'accounts', COUNT_BIG(*) FROM web.accounts WHERE status IS NOT NULL
    UNION ALL SELECT 'reviews', COUNT_BIG(*) FROM web.reviews WHERE rating >= 1
    UNION ALL SELECT 'fact_sales', COUNT_BIG(*) FROM dw.fact_sales WHERE line_total >= 0
    UNION ALL SELECT 'transactions', COUNT_BIG(*) FROM dbo.transactions WHERE total >= 0;
END
GO

-- customer rank via a correlated subquery per row (O(n^2)-ish)
CREATE OR ALTER PROCEDURE rpt.usp_GetCustomerRank
    @customer_key BIGINT
AS
BEGIN
    SET NOCOUNT ON;
    SELECT @customer_key AS customer_key,
           (SELECT net_spend_usd FROM dw.agg_customer_ltv WHERE customer_key=@customer_key) AS spend,
           (SELECT COUNT(*)+1 FROM dw.agg_customer_ltv x
            WHERE x.net_spend_usd > (SELECT net_spend_usd FROM dw.agg_customer_ltv WHERE customer_key=@customer_key)) AS rank_position;
END
GO

-- SELECT * everything a customer ever did, unbounded
CREATE OR ALTER PROCEDURE rpt.usp_EverySingleSale
    @customer_key BIGINT
AS
BEGIN
    SET NOCOUNT ON;
    SELECT * FROM dw.sales_wide WHERE customer_key = @customer_key ORDER BY occurred_at;
END
GO

-- product lookup that LIKEs every text column with wildcards
CREATE OR ALTER PROCEDURE rpt.usp_ProductLookup_LikeEverything
    @term NVARCHAR(100)
AS
BEGIN
    SET NOCOUNT ON;
    SELECT * FROM dw.dim_product
    WHERE title LIKE '%'+@term+'%' OR publisher LIKE '%'+@term+'%'
       OR developer LIKE '%'+@term+'%' OR category LIKE '%'+@term+'%'
       OR platform_name LIKE '%'+@term+'%' OR media_type LIKE '%'+@term+'%';
END
GO

-- deliberately heavy: scans sales_wide with an un-indexable predicate
CREATE OR ALTER PROCEDURE rpt.usp_ReportThatTakesForever
AS
BEGIN
    SET NOCOUNT ON;
    SELECT p.platform_name, COUNT_BIG(*) AS lines, SUM(sw.line_total_usd) AS revenue_usd
    FROM dw.sales_wide sw
    JOIN dw.dim_product p ON p.product_key = sw.product_key
    WHERE sw.product_title LIKE '%' + LOWER(sw.platform_name) + '%'   -- correlated LIKE, per-row, whole scan
    GROUP BY p.platform_name ORDER BY revenue_usd DESC;
END
GO

-- which shops are open: function on the date column
CREATE OR ALTER PROCEDURE rpt.usp_WhichShopsWereOpen
    @on_date DATE
AS
BEGIN
    SET NOCOUNT ON;
    SELECT shop_key, name, country_code
    FROM dw.dim_shop
    WHERE YEAR(opened_date) <= YEAR(@on_date)
      AND (closed_date IS NULL OR CONVERT(VARCHAR(10), closed_date, 120) >= CONVERT(VARCHAR(10), @on_date, 120))
    ORDER BY name;
END
GO

-- temp-table shuffle: same data copied through three #temps
CREATE OR ALTER PROCEDURE rpt.usp_TopCustomers_TempShuffle
    @top INT = 50
AS
BEGIN
    SET NOCOUNT ON;
    SELECT customer_key, net_spend_usd INTO #a FROM dw.agg_customer_ltv;
    SELECT * INTO #b FROM #a WHERE net_spend_usd > 0;
    SELECT * INTO #c FROM #b;
    SELECT TOP (@top) c.customer_key, c.net_spend_usd, dc.country_code
    FROM #c c JOIN dw.dim_customer dc ON dc.customer_key = c.customer_key
    ORDER BY c.net_spend_usd DESC;
END
GO

-- quarterly report: yet another view layer
CREATE OR ALTER PROCEDURE rpt.usp_QuarterlyReport_LegacyView
    @year SMALLINT = 2010
AS
BEGIN
    SET NOCOUNT ON;
    SELECT YEAR(occurred_at) AS yr, DATEPART(QUARTER, occurred_at) AS q,
           SUM(line_total_usd) AS revenue_usd
    FROM rpt.v_sales_final
    WHERE YEAR(occurred_at) = @year
    GROUP BY YEAR(occurred_at), DATEPART(QUARTER, occurred_at)
    ORDER BY q;
END
GO
