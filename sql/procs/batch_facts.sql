-- fact_sales (grain = transaction_lines with a product)
CREATE OR ALTER PROCEDURE batch.usp_refresh_fact_sales
    @full  BIT  = 1,
    @start DATE = NULL,
    @end   DATE = NULL
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT, @rows BIGINT = 0, @m DATE, @mend DATE, @hw BIGINT = 9000000000;
    INSERT dw.etl_run_log(proc_name,target_object,mode)
        VALUES(N'batch.usp_refresh_fact_sales',N'dw.fact_sales',CASE WHEN @full=1 THEN N'full' ELSE N'incremental' END);
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        IF @start IS NULL SELECT @start = MIN(occurred_at) FROM dbo.transactions;
        IF @end   IS NULL SELECT @end   = MAX(occurred_at) FROM dbo.transactions;
        IF @full = 1
        BEGIN
            TRUNCATE TABLE dw.fact_sales;
            -- disable secondary NC indexes for the bulk load (keeps random B-tree
            -- writes out of the month loop), rebuild them once at the end
            ALTER INDEX ix_fact_sales_product  ON dw.fact_sales DISABLE;
            ALTER INDEX ix_fact_sales_customer ON dw.fact_sales DISABLE;
            ALTER INDEX ix_fact_sales_shop     ON dw.fact_sales DISABLE;
            ALTER INDEX ix_fact_sales_date     ON dw.fact_sales DISABLE;
        END

        SET @m = DATEFROMPARTS(YEAR(@start),MONTH(@start),1);
        WHILE @m <= @end
        BEGIN
            SET @mend = DATEADD(MONTH,1,@m);
            IF @full = 0 DELETE dw.fact_sales WHERE occurred_at >= @m AND occurred_at < @mend;
            INSERT dw.fact_sales(transaction_line_id,transaction_id,occurred_at,date_key,product_key,customer_key,
                                 shop_key,staff_key,channel_key,condition_key,currency_code,quantity,unit_price,
                                 line_discount,line_tax,line_total,line_total_usd,is_return,is_hardware)
            SELECT tl.transaction_line_id, t.transaction_id, t.occurred_at,
                   CONVERT(INT,CONVERT(CHAR(8),t.occurred_at,112)),
                   CASE WHEN tl.release_id IS NOT NULL THEN tl.release_id ELSE @hw + tl.hardware_id END,
                   t.customer_id, t.shop_id, t.staff_id, ch.channel_key, co.condition_key, t.currency_code,
                   tl.quantity, tl.unit_price, tl.line_discount, tl.line_tax, tl.line_total,
                   CAST(tl.line_total / COALESCE(NULLIF(fx.rate_to_usd,0),1.0) AS DECIMAL(14,4)),
                   CASE WHEN t.original_transaction_id IS NOT NULL THEN 1 ELSE 0 END,
                   CASE WHEN tl.hardware_id IS NOT NULL THEN 1 ELSE 0 END
            FROM dbo.transactions t
            JOIN dbo.transaction_lines tl ON tl.transaction_id = t.transaction_id
            LEFT JOIN dw.dim_channel   ch ON ch.channel_name   = t.channel
            LEFT JOIN dw.dim_condition co ON co.condition_name = tl.condition
            LEFT JOIN dw.dim_fx        fx ON fx.currency_code  = t.currency_code AND fx.year = YEAR(t.occurred_at)
            WHERE t.occurred_at >= @m AND t.occurred_at < @mend
              AND (tl.release_id IS NOT NULL OR tl.hardware_id IS NOT NULL);
            SET @rows = @rows + @@ROWCOUNT;
            SET @m = @mend;
        END
        IF @full = 1
        BEGIN
            ALTER INDEX ix_fact_sales_product  ON dw.fact_sales REBUILD;
            ALTER INDEX ix_fact_sales_customer ON dw.fact_sales REBUILD;
            ALTER INDEX ix_fact_sales_shop     ON dw.fact_sales REBUILD;
            ALTER INDEX ix_fact_sales_date     ON dw.fact_sales REBUILD;
        END
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@rows,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- fact_tradein (grain = trade_in_items)
CREATE OR ALTER PROCEDURE batch.usp_refresh_fact_tradein
    @full BIT = 1, @start DATE = NULL, @end DATE = NULL
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT, @rows BIGINT = 0, @m DATE, @mend DATE, @hw BIGINT = 9000000000;
    INSERT dw.etl_run_log(proc_name,target_object,mode)
        VALUES(N'batch.usp_refresh_fact_tradein',N'dw.fact_tradein',CASE WHEN @full=1 THEN N'full' ELSE N'incremental' END);
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        IF @start IS NULL SELECT @start = MIN(occurred_at) FROM dbo.trade_ins;
        IF @end   IS NULL SELECT @end   = MAX(occurred_at) FROM dbo.trade_ins;
        IF @full = 1 TRUNCATE TABLE dw.fact_tradein;
        SET @m = DATEFROMPARTS(YEAR(@start),MONTH(@start),1);
        WHILE @m <= @end
        BEGIN
            SET @mend = DATEADD(MONTH,1,@m);
            IF @full = 0 DELETE dw.fact_tradein WHERE occurred_at >= @m AND occurred_at < @mend;
            INSERT dw.fact_tradein(trade_in_item_id,trade_in_id,occurred_at,date_key,product_key,customer_key,
                                   shop_key,condition_key,currency_code,offer_amount,offer_amount_usd,is_hardware)
            SELECT ti.trade_in_item_id, ti.trade_in_id, tr.occurred_at,
                   CONVERT(INT,CONVERT(CHAR(8),tr.occurred_at,112)),
                   CASE WHEN ti.release_id IS NOT NULL THEN ti.release_id
                        WHEN ti.hardware_id IS NOT NULL THEN @hw + ti.hardware_id ELSE NULL END,
                   tr.customer_id, tr.shop_id, co.condition_key, tr.currency_code,
                   ti.valuation,
                   CAST(ti.valuation / COALESCE(NULLIF(fx.rate_to_usd,0),1.0) AS DECIMAL(14,4)),
                   CASE WHEN ti.hardware_id IS NOT NULL THEN 1 ELSE 0 END
            FROM dbo.trade_ins tr
            JOIN dbo.trade_in_items ti ON ti.trade_in_id = tr.trade_in_id
            LEFT JOIN dw.dim_condition co ON co.condition_name = ti.condition
            LEFT JOIN dw.dim_fx fx ON fx.currency_code = tr.currency_code AND fx.year = YEAR(tr.occurred_at)
            WHERE tr.occurred_at >= @m AND tr.occurred_at < @mend;
            SET @rows = @rows + @@ROWCOUNT;
            SET @m = @mend;
        END
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@rows,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- fact_web_activity (grain = web.page_views)
CREATE OR ALTER PROCEDURE batch.usp_refresh_fact_web_activity
    @full BIT = 1, @start DATE = NULL, @end DATE = NULL
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT, @rows BIGINT = 0, @m DATE, @mend DATE;
    INSERT dw.etl_run_log(proc_name,target_object,mode)
        VALUES(N'batch.usp_refresh_fact_web_activity',N'dw.fact_web_activity',CASE WHEN @full=1 THEN N'full' ELSE N'incremental' END);
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        IF @start IS NULL SELECT @start = MIN(occurred_at) FROM web.page_views;
        IF @end   IS NULL SELECT @end   = MAX(occurred_at) FROM web.page_views;
        IF @full = 1 TRUNCATE TABLE dw.fact_web_activity;
        SET @m = DATEFROMPARTS(YEAR(@start),MONTH(@start),1);
        WHILE @m <= @end
        BEGIN
            SET @mend = DATEADD(MONTH,1,@m);
            IF @full = 0 DELETE dw.fact_web_activity WHERE occurred_at >= @m AND occurred_at < @mend;
            INSERT dw.fact_web_activity(page_view_id,session_id,occurred_at,date_key,account_id,customer_key,
                                        url_path,product_key,client_country,user_agent_family,is_bot,http_status,bytes_sent)
            SELECT pv.page_view_id, pv.session_id, pv.occurred_at,
                   CONVERT(INT,CONVERT(CHAR(8),pv.occurred_at,112)),
                   pv.account_id, a.customer_id, pv.url_path,
                   TRY_CONVERT(BIGINT, CASE WHEN pv.url_path LIKE '/reviews/[0-9]%'
                                            THEN SUBSTRING(pv.url_path, 10, 20) END),
                   pv.client_country, pv.user_agent_family,
                   CASE WHEN pv.user_agent_family IN (N'Googlebot',N'bingbot',N'YahooSlurp',N'crawler') THEN 1 ELSE 0 END,
                   pv.http_status, pv.bytes_sent
            FROM web.page_views pv
            LEFT JOIN web.accounts a ON a.account_id = pv.account_id
            WHERE pv.occurred_at >= @m AND pv.occurred_at < @mend;
            SET @rows = @rows + @@ROWCOUNT;
            SET @m = @mend;
        END
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@rows,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- orchestrator
CREATE OR ALTER PROCEDURE batch.usp_refresh_all_facts
    @full BIT = 1
AS
BEGIN
    SET NOCOUNT ON;
    EXEC batch.usp_refresh_fact_sales        @full = @full;
    EXEC batch.usp_refresh_fact_tradein      @full = @full;
    EXEC batch.usp_refresh_fact_web_activity @full = @full;
END
GO
