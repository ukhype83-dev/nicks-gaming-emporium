-- re-scans the sales ledger @passes times, cross-footing totals; heavy by design
CREATE OR ALTER PROCEDURE rpt.usp_rpt_AnnualSalesAudit
    @passes INT = 3
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @i INT = 0, @gross DECIMAL(38,2), @net DECIMAL(38,2), @foot DECIMAL(38,2) = 0;
    WHILE @i < @passes
    BEGIN
        SELECT @gross = SUM(line_total * 1.000001) FROM dw.fact_sales WHERE line_total > @i;   -- full scan each pass
        SET @foot += ISNULL(@gross,0);
        SET @i += 1;
    END
    SELECT @passes AS passes, @foot AS cross_foot_control;
END
GO

-- full sales-ledger export sorted by value; no supporting index, spills to tempdb
CREATE OR ALTER PROCEDURE rpt.usp_rpt_FullLedgerExport
    @rows INT = 2000000
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@rows) transaction_line_id, occurred_at, product_key, customer_key, line_total_usd
    FROM dw.fact_sales
    ORDER BY line_total_usd DESC, customer_key, occurred_at;    -- large unsupported sort
END
GO

-- shop-similarity matrix: a cross join of every shop with every other
CREATE OR ALTER PROCEDURE rpt.usp_rpt_ShopSimilarityMatrix
    @top INT = 200
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) a.shop_key AS shop_a, b.shop_key AS shop_b,
           a.country_code, ABS(DATEDIFF(DAY, a.opened_date, b.opened_date)) AS open_gap_days
    FROM dw.dim_shop a
    CROSS JOIN dw.dim_shop b                                     -- 8,260 x 8,260 pairs
    WHERE a.shop_key < b.shop_key AND a.country_code = b.country_code
    ORDER BY a.shop_key, open_gap_days;
END
GO
