CREATE OR ALTER PROCEDURE batch.usp_refresh_agg_sales_by_month
AS
BEGIN
    SET NOCOUNT ON; DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_agg_sales_by_month',N'dw.agg_sales_by_month',N'full');
    SET @run_id=SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.agg_sales_by_month;
        INSERT dw.agg_sales_by_month(year_month,line_count,tx_count,units,gross_revenue_usd,return_count,
                                     return_value_usd,net_revenue_usd,software_revenue_usd,hardware_revenue_usd,avg_line_usd)
        SELECT CONVERT(CHAR(7),occurred_at,126),
               COUNT_BIG(*), COUNT(DISTINCT transaction_id),
               SUM(CASE WHEN is_return=0 THEN quantity ELSE 0 END),
               SUM(CASE WHEN is_return=0 THEN line_total_usd ELSE 0 END),
               SUM(CASE WHEN is_return=1 THEN 1 ELSE 0 END),
               SUM(CASE WHEN is_return=1 THEN line_total_usd ELSE 0 END),
               SUM(line_total_usd),
               SUM(CASE WHEN is_hardware=0 THEN line_total_usd ELSE 0 END),
               SUM(CASE WHEN is_hardware=1 THEN line_total_usd ELSE 0 END),
               CAST(AVG(line_total_usd) AS DECIMAL(12,4))
        FROM dw.fact_sales GROUP BY CONVERT(CHAR(7),occurred_at,126);
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW; END CATCH
END
GO

CREATE OR ALTER PROCEDURE batch.usp_refresh_agg_sales_by_month_platform
AS
BEGIN
    SET NOCOUNT ON; DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_agg_sales_by_month_platform',N'dw.agg_sales_by_month_platform',N'full');
    SET @run_id=SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.agg_sales_by_month_platform;
        INSERT dw.agg_sales_by_month_platform(year_month,platform_key,platform_name,units,line_count,revenue_usd)
        SELECT CONVERT(CHAR(7),f.occurred_at,126), COALESCE(p.platform_key,-1), MAX(p.platform_name),
               SUM(CASE WHEN f.is_return=0 THEN f.quantity ELSE 0 END), COUNT_BIG(*), SUM(f.line_total_usd)
        FROM dw.fact_sales f LEFT JOIN dw.dim_product p ON p.product_key=f.product_key
        GROUP BY CONVERT(CHAR(7),f.occurred_at,126), COALESCE(p.platform_key,-1);
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW; END CATCH
END
GO

CREATE OR ALTER PROCEDURE batch.usp_refresh_agg_sales_by_day_shop
AS
BEGIN
    SET NOCOUNT ON; DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_agg_sales_by_day_shop',N'dw.agg_sales_by_day_shop',N'full');
    SET @run_id=SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.agg_sales_by_day_shop;
        INSERT dw.agg_sales_by_day_shop(date_key,shop_key,tx_count,line_count,units,revenue_usd)
        SELECT date_key, COALESCE(shop_key,-1), COUNT(DISTINCT transaction_id), COUNT_BIG(*),
               SUM(CASE WHEN is_return=0 THEN quantity ELSE 0 END), SUM(line_total_usd)
        FROM dw.fact_sales GROUP BY date_key, COALESCE(shop_key,-1);
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW; END CATCH
END
GO

CREATE OR ALTER PROCEDURE batch.usp_refresh_agg_product_performance
AS
BEGIN
    SET NOCOUNT ON; DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_agg_product_performance',N'dw.agg_product_performance',N'full');
    SET @run_id=SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.agg_product_performance;
        INSERT dw.agg_product_performance(product_key,first_sold,last_sold,units_sold,line_count,revenue_usd,return_count,distinct_customers)
        SELECT product_key, CAST(MIN(occurred_at) AS DATE), CAST(MAX(occurred_at) AS DATE),
               SUM(CASE WHEN is_return=0 THEN quantity ELSE 0 END), COUNT_BIG(*), SUM(line_total_usd),
               SUM(CASE WHEN is_return=1 THEN 1 ELSE 0 END), COUNT(DISTINCT customer_key)
        FROM dw.fact_sales GROUP BY product_key;
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW; END CATCH
END
GO

CREATE OR ALTER PROCEDURE batch.usp_refresh_agg_customer_ltv
AS
BEGIN
    SET NOCOUNT ON; DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_agg_customer_ltv',N'dw.agg_customer_ltv',N'full');
    SET @run_id=SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.agg_customer_ltv;
        INSERT dw.agg_customer_ltv(customer_key,first_purchase,last_purchase,order_count,line_count,units,gross_spend_usd,returns_usd,net_spend_usd)
        SELECT customer_key, CAST(MIN(occurred_at) AS DATE), CAST(MAX(occurred_at) AS DATE),
               COUNT(DISTINCT transaction_id), COUNT_BIG(*),
               SUM(CASE WHEN is_return=0 THEN quantity ELSE 0 END),
               SUM(CASE WHEN is_return=0 THEN line_total_usd ELSE 0 END),
               SUM(CASE WHEN is_return=1 THEN line_total_usd ELSE 0 END),
               SUM(line_total_usd)
        FROM dw.fact_sales WHERE customer_key IS NOT NULL GROUP BY customer_key;
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW; END CATCH
END
GO

CREATE OR ALTER PROCEDURE batch.usp_refresh_agg_web_traffic_daily
AS
BEGIN
    SET NOCOUNT ON; DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_agg_web_traffic_daily',N'dw.agg_web_traffic_daily',N'full');
    SET @run_id=SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.agg_web_traffic_daily;
        INSERT dw.agg_web_traffic_daily(date_key,page_views,sessions,bot_views,human_views,logged_in_views,distinct_countries)
        SELECT date_key, COUNT_BIG(*), COUNT(DISTINCT session_id),
               SUM(CAST(is_bot AS INT)), SUM(1-CAST(is_bot AS INT)),
               SUM(CASE WHEN account_id IS NOT NULL THEN 1 ELSE 0 END),
               COUNT(DISTINCT client_country)
        FROM dw.fact_web_activity GROUP BY date_key;
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW; END CATCH
END
GO

CREATE OR ALTER PROCEDURE batch.usp_refresh_agg_review_sentiment_by_month
AS
BEGIN
    SET NOCOUNT ON; DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_agg_review_sentiment_by_month',N'dw.agg_review_sentiment_by_month',N'full');
    SET @run_id=SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.agg_review_sentiment_by_month;
        INSERT dw.agg_review_sentiment_by_month(year_month,review_count,avg_rating,rating_only_count,verified_count)
        SELECT CONVERT(CHAR(7),posted_at,126), COUNT_BIG(*),
               CAST(AVG(CAST(rating AS FLOAT)) AS DECIMAL(4,3)),
               SUM(CASE WHEN body=N'' THEN 1 ELSE 0 END),
               SUM(CAST(is_verified_purchase AS INT))
        FROM web.reviews GROUP BY CONVERT(CHAR(7),posted_at,126);
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW; END CATCH
END
GO

-- orchestrators
CREATE OR ALTER PROCEDURE batch.usp_refresh_all_rollups
AS
BEGIN
    SET NOCOUNT ON;
    EXEC batch.usp_refresh_agg_sales_by_month;
    EXEC batch.usp_refresh_agg_sales_by_month_platform;
    EXEC batch.usp_refresh_agg_sales_by_day_shop;
    EXEC batch.usp_refresh_agg_product_performance;
    EXEC batch.usp_refresh_agg_customer_ltv;
    EXEC batch.usp_refresh_agg_web_traffic_daily;
    EXEC batch.usp_refresh_agg_review_sentiment_by_month;
END
GO

-- the overnight batch: dims → facts → rollups, end to end
CREATE OR ALTER PROCEDURE batch.usp_refresh_everything
    @full BIT = 1
AS
BEGIN
    SET NOCOUNT ON;
    EXEC batch.usp_refresh_all_dimensions;
    IF @full = 1 EXEC batch.usp_drop_columnstore;    -- keep the big fact reload fast
    EXEC batch.usp_refresh_all_facts @full = @full;
    EXEC batch.usp_refresh_all_rollups;
    EXEC batch.usp_refresh_sales_wide @full = @full; -- CCI OBT
    EXEC batch.usp_build_columnstore;                -- (re)create the NCCIs
END
GO
