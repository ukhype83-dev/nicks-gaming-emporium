-- control table the jobs stamp / lock
IF OBJECT_ID('batch.job_control') IS NULL
BEGIN
    CREATE TABLE batch.job_control (
        job_name    SYSNAME      NOT NULL PRIMARY KEY,
        status      NVARCHAR(16) NOT NULL DEFAULT 'idle',
        last_run_at DATETIME2(3) NULL,
        run_count   BIGINT       NOT NULL DEFAULT 0
    );
    INSERT batch.job_control(job_name) VALUES
        (N'supplier_price_feed'),(N'supplier_stock_poll'),(N'loyalty_recompute'),
        (N'customer_tier_recompute'),(N'month_end_close'),(N'dashboard_refresh'),
        (N'statement_run'),(N'eod_reconciliation');
END
GO

-- pulls the distributor price feed over the legacy linked server
CREATE OR ALTER PROCEDURE batch.usp_ImportSupplierPriceFeed
    @timeout_seconds INT = 30
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @d CHAR(8) = CONVERT(CHAR(8), DATEADD(SECOND, @timeout_seconds, 0), 108);
    WAITFOR DELAY @d;
    UPDATE batch.job_control SET status='ok', last_run_at=SYSUTCDATETIME(), run_count=run_count+1
        WHERE job_name=N'supplier_price_feed';
    SELECT @timeout_seconds AS feed_seconds, SYSUTCDATETIME() AS completed_at;
END
GO

-- polls the supplier stock-availability service
CREATE OR ALTER PROCEDURE batch.usp_PollSupplierStock
    @max_wait_seconds INT = 60
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @s INT = 1 + ABS(CHECKSUM(NEWID())) % @max_wait_seconds;
    DECLARE @d CHAR(8) = CONVERT(CHAR(8), DATEADD(SECOND, @s, 0), 108);
    WAITFOR DELAY @d;
    UPDATE batch.job_control SET status='ok', last_run_at=SYSUTCDATETIME(), run_count=run_count+1
        WHERE job_name=N'supplier_stock_poll';
    SELECT @s AS waited_seconds;
END
GO

-- recompute loyalty point balances
CREATE OR ALTER PROCEDURE batch.usp_RecalculateLoyaltyPoints
    @seconds INT = 30
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @end DATETIME2 = DATEADD(SECOND, @seconds, SYSUTCDATETIME());
    DECLARE @acc FLOAT = 1.0, @i BIGINT = 0;
    WHILE SYSUTCDATETIME() < @end
    BEGIN
        SET @acc = SQRT(ABS(@acc * 1.61803399 + @i)) + 1.0;
        SET @i += 1;
    END
    UPDATE batch.job_control SET status='ok', last_run_at=SYSUTCDATETIME(), run_count=run_count+1
        WHERE job_name=N'loyalty_recompute';
    SELECT @i AS members_processed;
END
GO

-- recompute each customer's loyalty tier from their spend, one customer at a time
CREATE OR ALTER PROCEDURE batch.usp_RecalculateCustomerTiers
    @customers INT = 5000
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @ck BIGINT, @spend DECIMAL(18,2), @done INT = 0;
    DECLARE c CURSOR LOCAL FAST_FORWARD FOR
        SELECT TOP (@customers) customer_key FROM dw.agg_customer_ltv ORDER BY net_spend_usd DESC;
    OPEN c; FETCH NEXT FROM c INTO @ck;
    WHILE @@FETCH_STATUS = 0
    BEGIN
        SELECT @spend = SUM(line_total_usd) FROM dw.fact_sales WHERE customer_key = @ck;  -- scan per member
        SET @done += 1;
        FETCH NEXT FROM c INTO @ck;
    END
    CLOSE c; DEALLOCATE c;
    UPDATE batch.job_control SET status='ok', last_run_at=SYSUTCDATETIME(), run_count=run_count+1
        WHERE job_name=N'customer_tier_recompute';
    SELECT @done AS members_retiered;
END
GO

-- builds the reporting date spine with a recursive CTE
CREATE OR ALTER PROCEDURE batch.usp_GenerateDateSpine
    @days INT = 500000
AS
BEGIN
    SET NOCOUNT ON;
    ;WITH spine AS (
        SELECT 1 AS n, CAST('1986-01-01' AS DATE) AS dt
        UNION ALL SELECT n+1, DATEADD(DAY,1,dt) FROM spine WHERE n < @days
    )
    SELECT COUNT_BIG(*) AS days_generated, MAX(dt) AS last_date FROM spine OPTION (MAXRECURSION 0);
END
GO

-- month-end close: takes the finance close lock, posts the period, releases
CREATE OR ALTER PROCEDURE batch.usp_MonthEndClose
    @hold_seconds INT = 30
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @d CHAR(8) = CONVERT(CHAR(8), DATEADD(SECOND, @hold_seconds, 0), 108);
    BEGIN TRAN;
        UPDATE batch.job_control SET status='closing', last_run_at=SYSUTCDATETIME(), run_count=run_count+1
            WHERE job_name=N'month_end_close';           -- X lock held for the close
        WAITFOR DELAY @d;
        UPDATE batch.job_control SET status='closed' WHERE job_name=N'month_end_close';
    COMMIT;
    SELECT @hold_seconds AS close_seconds;
END
GO

-- precompute the numbers the morning dashboard shows
CREATE OR ALTER PROCEDURE batch.usp_NightlyDashboardRefresh
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @a BIGINT,@b BIGINT,@c BIGINT,@e BIGINT;
    SELECT @a = COUNT_BIG(*) FROM dw.fact_sales WHERE is_hardware=1;
    SELECT @b = COUNT_BIG(*) FROM dw.fact_sales WHERE is_return=1;
    SELECT @c = COUNT_BIG(*) FROM dw.fact_web_activity WHERE is_bot=1;
    SELECT @e = COUNT_BIG(*) FROM dw.sales_wide WHERE line_total_usd > 50;
    UPDATE batch.job_control SET status='ok', last_run_at=SYSUTCDATETIME(), run_count=run_count+1
        WHERE job_name=N'dashboard_refresh';
    SELECT @a AS hardware_lines, @b AS returns, @c AS bot_views, @e AS big_lines;
END
GO

-- monthly statement run: sends statements one at a time, throttled to one per second
CREATE OR ALTER PROCEDURE batch.usp_EmailMonthlyStatements
    @count INT = 30
AS
BEGIN
    SET NOCOUNT ON;
    CREATE TABLE #sent (n INT, at DATETIME2);
    DECLARE @i INT = 0;
    WHILE @i < @count
    BEGIN
        INSERT #sent VALUES (@i, SYSUTCDATETIME());
        WAITFOR DELAY '00:00:01';
        SET @i += 1;
    END
    UPDATE batch.job_control SET status='ok', last_run_at=SYSUTCDATETIME(), run_count=run_count+1
        WHERE job_name=N'statement_run';
    SELECT COUNT(*) AS statements_sent FROM #sent;
END
GO

-- end-of-day reconciliation: recompute + audit pass + settle
CREATE OR ALTER PROCEDURE batch.usp_EndOfDayReconciliation
    @seconds INT = 45
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @third INT = @seconds / 3;
    EXEC batch.usp_RecalculateLoyaltyPoints @seconds = @third;
    EXEC rpt.usp_rpt_AnnualSalesAudit @passes = 2;
    DECLARE @d CHAR(8) = CONVERT(CHAR(8), DATEADD(SECOND, @third, 0), 108);
    WAITFOR DELAY @d;
    UPDATE batch.job_control SET status='ok', last_run_at=SYSUTCDATETIME(), run_count=run_count+1
        WHERE job_name=N'eod_reconciliation';
END
GO
