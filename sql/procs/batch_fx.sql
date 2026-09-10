-- build the dense as-of FX dimension (currency × year)
CREATE OR ALTER PROCEDURE batch.usp_refresh_dim_fx
    @start_year SMALLINT = 1986, @end_year SMALLINT = 2016
AS
BEGIN
    SET NOCOUNT ON; DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_dim_fx',N'dw.dim_fx',N'full');
    SET @run_id=SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.dim_fx;
        ;WITH yrs AS (
            SELECT TOP (@end_year-@start_year+1)
                   CAST(@start_year + ROW_NUMBER() OVER (ORDER BY (SELECT NULL)) - 1 AS SMALLINT) AS yr
            FROM sys.all_objects
        ),
        curr AS (SELECT DISTINCT currency_code FROM dbo.fx_rates)
        INSERT dw.dim_fx(currency_code, year, rate_to_usd)
        SELECT c.currency_code, y.yr,
               COALESCE(
                 (SELECT TOP 1 f.rate_to_usd FROM dbo.fx_rates f
                    WHERE f.currency_code=c.currency_code AND f.effective_year<=y.yr
                    ORDER BY f.effective_year DESC),
                 (SELECT TOP 1 f.rate_to_usd FROM dbo.fx_rates f
                    WHERE f.currency_code=c.currency_code
                    ORDER BY f.effective_year ASC))
        FROM curr c CROSS JOIN yrs y;
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- correct line_total_usd / offer_amount_usd in place, month by month, from dim_fx
-- (NCCIs carry these columns, so drop them first — usp_fix_all_usd does)
CREATE OR ALTER PROCEDURE batch.usp_fix_fact_usd
AS
BEGIN
    SET NOCOUNT ON; DECLARE @run_id BIGINT, @rows BIGINT = 0, @m DATE, @mend DATE, @end DATE;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_fix_fact_usd',N'dw.fact_sales/tradein',N'full');
    SET @run_id=SCOPE_IDENTITY();
    BEGIN TRY
        SELECT @m = DATEFROMPARTS(YEAR(MIN(occurred_at)),MONTH(MIN(occurred_at)),1), @end = MAX(occurred_at) FROM dw.fact_sales;
        WHILE @m <= @end
        BEGIN
            SET @mend = DATEADD(MONTH,1,@m);
            UPDATE f SET f.line_total_usd = CAST(f.line_total / COALESCE(NULLIF(x.rate_to_usd,0),1.0) AS DECIMAL(14,4))
            FROM dw.fact_sales f
            LEFT JOIN dw.dim_fx x ON x.currency_code=f.currency_code AND x.year=YEAR(f.occurred_at)
            WHERE f.occurred_at >= @m AND f.occurred_at < @mend;
            SET @rows = @rows + @@ROWCOUNT;
            SET @m = @mend;
        END
        -- fact_tradein (smaller; single pass is fine)
        UPDATE t SET t.offer_amount_usd = CAST(t.offer_amount / COALESCE(NULLIF(x.rate_to_usd,0),1.0) AS DECIMAL(14,4))
        FROM dw.fact_tradein t
        LEFT JOIN dw.dim_fx x ON x.currency_code=t.currency_code AND x.year=YEAR(t.occurred_at);
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@rows,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- one-shot USD correction for an already-built DW:
-- dim_fx → drop NCCIs → fix fact USD → re-agg rollups → rebuild OBT → rebuild NCCIs
CREATE OR ALTER PROCEDURE batch.usp_fix_all_usd
AS
BEGIN
    SET NOCOUNT ON;
    EXEC batch.usp_refresh_dim_fx;
    EXEC batch.usp_drop_columnstore;
    EXEC batch.usp_fix_fact_usd;
    EXEC batch.usp_refresh_all_rollups;
    EXEC batch.usp_refresh_sales_wide @full = 1;
    EXEC batch.usp_build_columnstore;
END
GO
