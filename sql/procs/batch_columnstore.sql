-- sales_wide OBT load: denormalise fact_sales + dims into the CCI
CREATE OR ALTER PROCEDURE batch.usp_refresh_sales_wide
    @full BIT = 1, @start DATE = NULL, @end DATE = NULL
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT, @rows BIGINT = 0, @m DATE, @mend DATE;
    INSERT dw.etl_run_log(proc_name,target_object,mode)
        VALUES(N'batch.usp_refresh_sales_wide',N'dw.sales_wide',CASE WHEN @full=1 THEN N'full' ELSE N'incremental' END);
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        IF @start IS NULL SELECT @start = MIN(occurred_at) FROM dw.fact_sales;
        IF @end   IS NULL SELECT @end   = MAX(occurred_at) FROM dw.fact_sales;
        IF @full = 1 TRUNCATE TABLE dw.sales_wide;
        SET @m = DATEFROMPARTS(YEAR(@start),MONTH(@start),1);
        WHILE @m <= @end
        BEGIN
            SET @mend = DATEADD(MONTH,1,@m);
            IF @full = 0 DELETE dw.sales_wide WHERE occurred_at >= @m AND occurred_at < @mend;
            INSERT dw.sales_wide(transaction_line_id,transaction_id,occurred_at,date_key,year,month,
                product_key,product_title,product_kind,platform_name,genre,publisher,
                customer_key,customer_country,customer_region,loyalty_tier,
                shop_key,shop_name,shop_country,shop_region,channel_name,condition_name,currency_code,
                quantity,line_total,line_total_usd,is_return,is_hardware)
            SELECT f.transaction_line_id,f.transaction_id,f.occurred_at,f.date_key,
                   YEAR(f.occurred_at),MONTH(f.occurred_at),
                   f.product_key,p.title,p.product_kind,p.platform_name,p.category,p.publisher,
                   f.customer_key,c.country_code,c.region,c.loyalty_tier,
                   f.shop_key,s.name,s.country_code,s.region,ch.channel_name,co.condition_name,f.currency_code,
                   f.quantity,f.line_total,f.line_total_usd,f.is_return,f.is_hardware
            FROM dw.fact_sales f
            LEFT JOIN dw.dim_product   p  ON p.product_key   = f.product_key
            LEFT JOIN dw.dim_customer  c  ON c.customer_key  = f.customer_key
            LEFT JOIN dw.dim_shop      s  ON s.shop_key      = f.shop_key
            LEFT JOIN dw.dim_channel   ch ON ch.channel_key  = f.channel_key
            LEFT JOIN dw.dim_condition co ON co.condition_key= f.condition_key
            WHERE f.occurred_at >= @m AND f.occurred_at < @mend;
            SET @rows = @rows + @@ROWCOUNT;
            SET @m = @mend;
        END
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@rows,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- create NCCIs on the rowstore facts + big rollups (idempotent)
CREATE OR ALTER PROCEDURE batch.usp_build_columnstore
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_build_columnstore',N'(NCCIs)',N'full');
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_fact_sales' AND object_id=OBJECT_ID('dw.fact_sales'))
            CREATE NONCLUSTERED COLUMNSTORE INDEX ncci_fact_sales ON dw.fact_sales
                (occurred_at,date_key,product_key,customer_key,shop_key,channel_key,condition_key,
                 quantity,line_total,line_total_usd,is_return,is_hardware);
        IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_fact_tradein' AND object_id=OBJECT_ID('dw.fact_tradein'))
            CREATE NONCLUSTERED COLUMNSTORE INDEX ncci_fact_tradein ON dw.fact_tradein
                (occurred_at,date_key,product_key,customer_key,shop_key,condition_key,offer_amount_usd,is_hardware);
        IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_fact_web' AND object_id=OBJECT_ID('dw.fact_web_activity'))
            CREATE NONCLUSTERED COLUMNSTORE INDEX ncci_fact_web ON dw.fact_web_activity
                (occurred_at,date_key,account_id,customer_key,product_key,is_bot,http_status,bytes_sent);
        IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_agg_customer_ltv' AND object_id=OBJECT_ID('dw.agg_customer_ltv'))
            CREATE NONCLUSTERED COLUMNSTORE INDEX ncci_agg_customer_ltv ON dw.agg_customer_ltv
                (order_count,units,gross_spend_usd,returns_usd,net_spend_usd);
        IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_agg_day_shop' AND object_id=OBJECT_ID('dw.agg_sales_by_day_shop'))
            CREATE NONCLUSTERED COLUMNSTORE INDEX ncci_agg_day_shop ON dw.agg_sales_by_day_shop
                (date_key,shop_key,tx_count,line_count,units,revenue_usd);
        IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_agg_product_perf' AND object_id=OBJECT_ID('dw.agg_product_performance'))
            CREATE NONCLUSTERED COLUMNSTORE INDEX ncci_agg_product_perf ON dw.agg_product_performance
                (product_key,units_sold,line_count,revenue_usd,return_count,distinct_customers);
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- drop the NCCIs before a full fact reload, so it stays fast
CREATE OR ALTER PROCEDURE batch.usp_drop_columnstore
AS
BEGIN
    SET NOCOUNT ON;
    IF EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_fact_sales' AND object_id=OBJECT_ID('dw.fact_sales'))
        DROP INDEX ncci_fact_sales ON dw.fact_sales;
    IF EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_fact_tradein' AND object_id=OBJECT_ID('dw.fact_tradein'))
        DROP INDEX ncci_fact_tradein ON dw.fact_tradein;
    IF EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_fact_web' AND object_id=OBJECT_ID('dw.fact_web_activity'))
        DROP INDEX ncci_fact_web ON dw.fact_web_activity;
    IF EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_agg_customer_ltv' AND object_id=OBJECT_ID('dw.agg_customer_ltv'))
        DROP INDEX ncci_agg_customer_ltv ON dw.agg_customer_ltv;
    IF EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_agg_day_shop' AND object_id=OBJECT_ID('dw.agg_sales_by_day_shop'))
        DROP INDEX ncci_agg_day_shop ON dw.agg_sales_by_day_shop;
    IF EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_agg_product_perf' AND object_id=OBJECT_ID('dw.agg_product_performance'))
        DROP INDEX ncci_agg_product_perf ON dw.agg_product_performance;
END
GO

-- maintenance: compress open/delta rowgroups
CREATE OR ALTER PROCEDURE batch.usp_rebuild_columnstore
AS
BEGIN
    SET NOCOUNT ON;
    ALTER INDEX cci_sales_wide ON dw.sales_wide REORGANIZE WITH (COMPRESS_ALL_ROW_GROUPS = ON);
    IF EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_fact_sales' AND object_id=OBJECT_ID('dw.fact_sales'))
        ALTER INDEX ncci_fact_sales ON dw.fact_sales REORGANIZE WITH (COMPRESS_ALL_ROW_GROUPS = ON);
    IF EXISTS (SELECT 1 FROM sys.indexes WHERE name='ncci_fact_web' AND object_id=OBJECT_ID('dw.fact_web_activity'))
        ALTER INDEX ncci_fact_web ON dw.fact_web_activity REORGANIZE WITH (COMPRESS_ALL_ROW_GROUPS = ON);
END
GO
