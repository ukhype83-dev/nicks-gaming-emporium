CREATE OR ALTER PROCEDURE rpt.usp_rpt_StockByShop
    @shop_key BIGINT
AS
BEGIN
    SET NOCOUNT ON;
    SELECT i.condition,
           COUNT(*) AS sku_rows,
           SUM(i.on_hand) AS on_hand, SUM(i.on_order) AS on_order, SUM(i.reserved) AS reserved
    FROM dbo.inventory i
    WHERE i.shop_id = @shop_key
    GROUP BY i.condition ORDER BY on_hand DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_StockByPlatform
    @shop_key BIGINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ISNULL(p.platform_name, N'(unknown)') AS platform_name,
           SUM(i.on_hand) AS on_hand, COUNT(*) AS sku_rows
    FROM dbo.inventory i
    JOIN dw.dim_product p ON p.product_key = CASE WHEN i.release_id IS NOT NULL THEN i.release_id
                                                  ELSE 9000000000 + i.hardware_id END
    WHERE (@shop_key IS NULL OR i.shop_id = @shop_key)
    GROUP BY p.platform_name ORDER BY on_hand DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_DeadStock
    @as_of DATE = '2016-09-30', @stale_days INT = 365, @top INT = 100
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) i.shop_id, p.title, p.platform_name, i.condition, i.on_hand, i.last_movement_at
    FROM dbo.inventory i
    JOIN dw.dim_product p ON p.product_key = CASE WHEN i.release_id IS NOT NULL THEN i.release_id
                                                  ELSE 9000000000 + i.hardware_id END
    WHERE i.on_hand > 0
      AND (i.last_movement_at IS NULL OR i.last_movement_at < DATEADD(DAY, -@stale_days, @as_of))
    ORDER BY i.on_hand DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_LowStockAlerts
    @shop_key BIGINT, @threshold INT = 2
AS
BEGIN
    SET NOCOUNT ON;
    SELECT p.title, p.platform_name, i.condition, i.on_hand, i.on_order
    FROM dbo.inventory i
    JOIN dw.dim_product p ON p.product_key = CASE WHEN i.release_id IS NOT NULL THEN i.release_id
                                                  ELSE 9000000000 + i.hardware_id END
    WHERE i.shop_id = @shop_key AND i.on_hand <= @threshold AND i.condition = 'new'
    ORDER BY i.on_hand;
END
GO
