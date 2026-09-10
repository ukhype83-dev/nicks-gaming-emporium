/* ---- fire a random long-runner (for monitoring-tool exercising) ---- */
CREATE OR ALTER PROCEDURE loadgen.usp_StressTest
    @seconds INT = 30
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @which INT = 1 + ABS(CHECKSUM(NEWID())) % 6;
    IF @which = 1 EXEC batch.usp_ImportSupplierPriceFeed @timeout_seconds = @seconds;
    ELSE IF @which = 2 EXEC batch.usp_RecalculateLoyaltyPoints @seconds = @seconds;
    ELSE IF @which = 3 EXEC rpt.usp_rpt_AnnualSalesAudit @passes = 2;
    ELSE IF @which = 4 EXEC rpt.usp_rpt_FullLedgerExport @rows = 1000000;
    ELSE IF @which = 5 EXEC batch.usp_RecalculateCustomerTiers @customers = 2000;
    ELSE EXEC batch.usp_MonthEndClose @hold_seconds = @seconds;
END
GO

/* ---- a burst of clean reports back to back (read storm) ---- */
CREATE OR ALTER PROCEDURE loadgen.usp_ReportStorm
    @iterations INT = 25
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @i INT = 0;
    WHILE @i < @iterations
    BEGIN
        EXEC loadgen.usp_RandomReport @chaos = 0;   -- clean reports only
        SET @i += 1;
    END
END
GO

/* ---- simulate the overnight batch (write load on the reporting layer) ---- */
CREATE OR ALTER PROCEDURE loadgen.usp_OvernightBatchSim
AS
BEGIN
    SET NOCOUNT ON;
    EXEC batch.usp_refresh_all_rollups;   -- re-aggregate (idempotent)
    EXEC batch.usp_UpdateStatsAll;        -- stats hygiene
    EXEC batch.usp_ColumnstoreHealth;     -- health snapshot
END
GO

/* ---- a mixed day: reports + the occasional heavy one ---- */
CREATE OR ALTER PROCEDURE loadgen.usp_MixedTradingDay
    @iterations INT = 20, @chaos INT = 30
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @i INT = 0;
    WHILE @i < @iterations
    BEGIN
        EXEC loadgen.usp_RandomReport @chaos = @chaos;
        IF (ABS(CHECKSUM(NEWID())) % 10) = 0 EXEC rpt.usp_rpt_AnnualSalesAudit @passes = 1;  -- occasional heavy one
        SET @i += 1;
    END
END
GO

/* ---- named preset for a monitoring soak test ---- */
CREATE OR ALTER PROCEDURE loadgen.usp_MonitoringSoak
    @rounds INT = 5, @seconds INT = 20
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @i INT = 0;
    WHILE @i < @rounds
    BEGIN
        EXEC loadgen.usp_StressTest @seconds = @seconds;
        SET @i += 1;
    END
END
GO
