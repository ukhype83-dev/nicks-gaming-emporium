-- refresh statistics on the dw tables (post-load hygiene)
CREATE OR ALTER PROCEDURE batch.usp_UpdateStatsAll
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @sql NVARCHAR(MAX) = N'';
    SELECT @sql = @sql + N'UPDATE STATISTICS ' + QUOTENAME(s.name) + N'.' + QUOTENAME(t.name) + N';' + CHAR(10)
    FROM sys.tables t JOIN sys.schemas s ON s.schema_id = t.schema_id
    WHERE s.name = 'dw';
    EXEC sys.sp_executesql @sql;
END
GO

-- index fragmentation over the dw (rowstore)
CREATE OR ALTER PROCEDURE batch.usp_IndexFragmentationReport
    @min_pages INT = 1000
AS
BEGIN
    SET NOCOUNT ON;
    SELECT OBJECT_NAME(ips.object_id) AS table_name, i.name AS index_name,
           ips.index_type_desc,
           CAST(ips.avg_fragmentation_in_percent AS DECIMAL(5,1)) AS frag_pct,
           ips.page_count
    FROM sys.dm_db_index_physical_stats(DB_ID(), NULL, NULL, NULL, 'LIMITED') ips
    JOIN sys.indexes i ON i.object_id = ips.object_id AND i.index_id = ips.index_id
    JOIN sys.objects o ON o.object_id = ips.object_id
    JOIN sys.schemas sc ON sc.schema_id = o.schema_id
    WHERE sc.name = 'dw' AND ips.page_count >= @min_pages AND ips.index_type_desc LIKE '%INDEX%'
    ORDER BY ips.avg_fragmentation_in_percent DESC;
END
GO

-- columnstore rowgroup health (open/compressed, deleted rows)
CREATE OR ALTER PROCEDURE batch.usp_ColumnstoreHealth
AS
BEGIN
    SET NOCOUNT ON;
    SELECT OBJECT_NAME(rg.object_id) AS table_name,
           rg.state_desc,
           COUNT(*) AS row_groups,
           SUM(rg.total_rows) AS total_rows,
           SUM(rg.deleted_rows) AS deleted_rows,
           CAST(SUM(rg.size_in_bytes)/1024.0/1024 AS DECIMAL(12,1)) AS size_mb
    FROM sys.dm_db_column_store_row_group_physical_stats rg
    JOIN sys.objects o ON o.object_id = rg.object_id
    JOIN sys.schemas sc ON sc.schema_id = o.schema_id
    WHERE sc.name = 'dw'
    GROUP BY rg.object_id, rg.state_desc
    ORDER BY table_name, rg.state_desc;
END
GO

-- dw table sizes
CREATE OR ALTER PROCEDURE batch.usp_TableSizes
AS
BEGIN
    SET NOCOUNT ON;
    SELECT s.name AS sch, t.name AS table_name,
           SUM(ps.row_count) AS rows_,
           CAST(SUM(ps.reserved_page_count)*8.0/1024 AS DECIMAL(14,1)) AS reserved_mb
    FROM sys.dm_db_partition_stats ps
    JOIN sys.tables t ON t.object_id = ps.object_id
    JOIN sys.schemas s ON s.schema_id = t.schema_id
    WHERE s.name = 'dw' AND ps.index_id IN (0,1,5)
    GROUP BY s.name, t.name ORDER BY reserved_mb DESC;
END
GO

-- recent ETL run history (from the run log)
CREATE OR ALTER PROCEDURE batch.usp_EtlRunHistory
    @top INT = 40
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) run_id, proc_name, target_object, mode, status,
           rows_affected, DATEDIFF(second, started_at, finished_at) AS secs, started_at
    FROM dw.etl_run_log ORDER BY run_id DESC;
END
GO

-- housekeeping: trim the ETL log
CREATE OR ALTER PROCEDURE batch.usp_PurgeEtlLog
    @keep_days INT = 90
AS
BEGIN
    SET NOCOUNT ON;
    DELETE FROM dw.etl_run_log WHERE started_at < DATEADD(DAY, -@keep_days, SYSUTCDATETIME());
    SELECT @@ROWCOUNT AS rows_purged;
END
GO

-- data freshness: latest fact dates vs OLTP source
CREATE OR ALTER PROCEDURE batch.usp_CheckDataFreshness
AS
BEGIN
    SET NOCOUNT ON;
    SELECT 'fact_sales' AS fact, (SELECT MAX(occurred_at) FROM dw.fact_sales) AS dw_max,
           (SELECT MAX(occurred_at) FROM dbo.transactions) AS oltp_max
    UNION ALL
    SELECT 'fact_web_activity', (SELECT MAX(occurred_at) FROM dw.fact_web_activity),
           (SELECT MAX(occurred_at) FROM web.page_views)
    UNION ALL
    SELECT 'fact_tradein', (SELECT MAX(occurred_at) FROM dw.fact_tradein),
           (SELECT MAX(occurred_at) FROM dbo.trade_ins);
END
GO
