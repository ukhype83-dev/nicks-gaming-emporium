-- one random report, weighted by the chaos dial
CREATE OR ALTER PROCEDURE loadgen.usp_RandomReport
    @chaos INT = 15                 -- 0 = only clean reports; 100 = mostly the heavy ones
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @year SMALLINT = 1995 + ABS(CHECKSUM(NEWID())) % 22;
    DECLARE @pk BIGINT   = (SELECT TOP 1 product_key  FROM dw.dim_product  ORDER BY NEWID());
    DECLARE @ck BIGINT   = (SELECT TOP 1 customer_key FROM dw.agg_customer_ltv ORDER BY NEWID());
    DECLARE @cc CHAR(2)  = (SELECT TOP 1 country_code FROM dw.dim_geography ORDER BY NEWID());
    DECLARE @roll INT    = 1 + ABS(CHECKSUM(NEWID())) % 100;
    DECLARE @bad  BIT    = CASE WHEN @roll <= @chaos THEN 1 ELSE 0 END;

    IF @bad = 1
    BEGIN
        DECLARE @which INT = 1 + ABS(CHECKSUM(NEWID())) % 5;
        IF @which = 1 EXEC rpt.usp_ToddsSalesReport @year = @year;
        ELSE IF @which = 2 EXEC rpt.usp_MonthlyNumberForBoss;
        ELSE IF @which = 3 EXEC rpt.usp_FindGamesLikeThis @term = N'the';
        ELSE IF @which = 4 EXEC rpt.usp_WhoBoughtWhat @product_key = @pk;
        ELSE EXEC rpt.usp_CustomerSearch @country = @cc;
    END
    ELSE
    BEGIN
        DECLARE @g INT = 1 + ABS(CHECKSUM(NEWID())) % 8;
        IF @g = 1 EXEC rpt.usp_rpt_SalesByPlatform;
        ELSE IF @g = 2 EXEC rpt.usp_rpt_TopSellers @top = 25;
        ELSE IF @g = 3 EXEC rpt.usp_rpt_SalesByRegion @year = @year;
        ELSE IF @g = 4 EXEC rpt.usp_rpt_TopSpenders @top = 25, @country = @cc;
        ELSE IF @g = 5 EXEC rpt.usp_rpt_ReviewSentimentTrend;
        ELSE IF @g = 6 EXEC rpt.usp_rpt_WagesPctRevenue;
        ELSE IF @g = 7 EXEC rpt.usp_rpt_CustomerLTV @customer_key = @ck;
        ELSE EXEC rpt.usp_rpt_ChannelMix @year = @year;
    END
END
GO

/* ---- a simulated analyst session: N random reports ---- */
CREATE OR ALTER PROCEDURE loadgen.usp_AnalystSession
    @iterations INT = 10, @chaos INT = 15
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @i INT = 0;
    WHILE @i < @iterations
    BEGIN
        EXEC loadgen.usp_RandomReport @chaos = @chaos;
        SET @i += 1;
    END
END
GO

/* ---- write load: reprocess the reporting layer (idempotent) ---- */
CREATE OR ALTER PROCEDURE loadgen.usp_BatchCycle
    @what NVARCHAR(20) = 'rollups'   -- 'rollups' | 'facts' | 'everything'
AS
BEGIN
    SET NOCOUNT ON;
    IF @what = 'everything' EXEC batch.usp_refresh_everything @full = 0;
    ELSE IF @what = 'facts' EXEC batch.usp_refresh_all_facts @full = 0;
    ELSE EXEC batch.usp_refresh_all_rollups;
END
GO

-- named presets:
--   QuietTuesday   : light clean read load
--   MonthEnd       : month-end close reports (incl. the slow ones)
--   BlackFriday    : heavy concurrent sales + traffic reporting
--   ToddRanAReport : the heavy/slow procs
CREATE OR ALTER PROCEDURE loadgen.usp_ChaosPreset
    @preset NVARCHAR(20) = 'QuietTuesday'
AS
BEGIN
    SET NOCOUNT ON;
    IF @preset = 'MonthEnd'
    BEGIN
        EXEC rpt.usp_rpt_PnL;
        EXEC rpt.usp_rpt_WagesPctRevenue;
        EXEC rpt.usp_rpt_WarehouseVsFinance;
        EXEC rpt.usp_MonthlyNumberForBoss;
        EXEC loadgen.usp_AnalystSession @iterations = 8, @chaos = 25;
    END
    ELSE IF @preset = 'BlackFriday'
    BEGIN
        EXEC rpt.usp_rpt_SalesByPlatform;
        EXEC rpt.usp_rpt_SalesByRegion;
        EXEC rpt.usp_rpt_ChannelMix;
        EXEC rpt.usp_rpt_TrafficByDay;
        EXEC loadgen.usp_AnalystSession @iterations = 20, @chaos = 20;
    END
    ELSE IF @preset = 'ToddRanAReport'
    BEGIN
        DECLARE @pk BIGINT = (SELECT TOP 1 product_key FROM dw.dim_product ORDER BY NEWID());
        EXEC rpt.usp_ToddsSalesReport;                  -- SELECT *, UDF per row, full scan
        EXEC rpt.usp_GetTopCustomersSLOW @top = 20;     -- the cursor
        EXEC rpt.usp_WhoBoughtWhat @product_key = @pk;
    END
    ELSE  -- QuietTuesday
    BEGIN
        EXEC loadgen.usp_AnalystSession @iterations = 5, @chaos = 5;
    END
END
GO
