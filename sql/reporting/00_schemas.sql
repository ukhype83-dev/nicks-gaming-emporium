-- Reporting / data-warehouse layer: dw, rpt, batch, loadgen schemas. Idempotent — safe to re-run.
-- OLTP (dbo/hr/web/finance) is read-only; writes are confined to dw (by batch/loadgen).

IF SCHEMA_ID('dw')      IS NULL EXEC('CREATE SCHEMA dw AUTHORIZATION dbo');
GO
IF SCHEMA_ID('rpt')     IS NULL EXEC('CREATE SCHEMA rpt AUTHORIZATION dbo');
GO
IF SCHEMA_ID('batch')   IS NULL EXEC('CREATE SCHEMA batch AUTHORIZATION dbo');
GO
IF SCHEMA_ID('loadgen') IS NULL EXEC('CREATE SCHEMA loadgen AUTHORIZATION dbo');
GO

-- batch run log: one row per refresh proc run (start/end/rows/status)
IF OBJECT_ID('dw.etl_run_log') IS NULL
CREATE TABLE dw.etl_run_log (
    run_id        BIGINT IDENTITY(1,1) NOT NULL PRIMARY KEY,
    proc_name     SYSNAME        NOT NULL,
    target_object SYSNAME        NULL,
    mode          NVARCHAR(16)   NOT NULL,           -- 'full' | 'incremental'
    started_at    DATETIME2(3)   NOT NULL DEFAULT SYSUTCDATETIME(),
    finished_at   DATETIME2(3)   NULL,
    rows_affected BIGINT         NULL,
    status        NVARCHAR(16)   NOT NULL DEFAULT 'running',  -- 'running'|'ok'|'error'
    message       NVARCHAR(2000) NULL
);
GO
