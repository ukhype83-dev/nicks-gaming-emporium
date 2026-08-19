CREATE OR ALTER PROCEDURE rpt.usp_rpt_TradeInSummary
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT YEAR(occurred_at) AS yr, MONTH(occurred_at) AS mo,
           COUNT(*) AS items,
           SUM(CASE WHEN is_hardware=1 THEN 1 ELSE 0 END) AS hardware_items,
           CAST(SUM(offer_amount_usd) AS DECIMAL(16,2)) AS offered_usd
    FROM dw.fact_tradein
    WHERE (@year IS NULL OR YEAR(occurred_at) = @year)
    GROUP BY YEAR(occurred_at), MONTH(occurred_at)
    ORDER BY yr, mo;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_TradeInByPlatform
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT ISNULL(p.platform_name, N'(unknown)') AS platform_name,
           COUNT(*) AS items, CAST(SUM(t.offer_amount_usd) AS DECIMAL(16,2)) AS offered_usd
    FROM dw.fact_tradein t
    LEFT JOIN dw.dim_product p ON p.product_key = t.product_key
    WHERE (@year IS NULL OR YEAR(t.occurred_at) = @year)
    GROUP BY p.platform_name ORDER BY offered_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_TradeInTopProducts
    @top INT = 25
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) p.title, p.platform_name, p.product_kind,
           COUNT(*) AS times_traded, CAST(AVG(t.offer_amount_usd) AS DECIMAL(12,2)) AS avg_offer_usd
    FROM dw.fact_tradein t
    JOIN dw.dim_product p ON p.product_key = t.product_key
    GROUP BY p.title, p.platform_name, p.product_kind
    ORDER BY times_traded DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_PayoutMethodMix
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT tr.payout_method,
           COUNT(*) AS trade_ins,
           CAST(SUM(tr.total_value / COALESCE(NULLIF(fx.rate_to_usd,0),1.0)) AS DECIMAL(16,2)) AS value_usd
    FROM dbo.trade_ins tr
    LEFT JOIN dw.dim_fx fx ON fx.currency_code = tr.currency_code AND fx.year = YEAR(tr.occurred_at)
    WHERE (@year IS NULL OR YEAR(tr.occurred_at) = @year)
    GROUP BY tr.payout_method ORDER BY value_usd DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_TradeInAttachToSale
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    -- what share of trade-ins were blended with a purchase (upgrade cycle)
    SELECT YEAR(occurred_at) AS yr,
           COUNT(*) AS trade_ins,
           SUM(CASE WHEN transaction_id IS NOT NULL THEN 1 ELSE 0 END) AS with_purchase,
           CAST(100.0*SUM(CASE WHEN transaction_id IS NOT NULL THEN 1 ELSE 0 END)/NULLIF(COUNT(*),0) AS DECIMAL(5,1)) AS attach_pct
    FROM dbo.trade_ins
    WHERE (@year IS NULL OR YEAR(occurred_at) = @year)
    GROUP BY YEAR(occurred_at) ORDER BY yr;
END
GO
