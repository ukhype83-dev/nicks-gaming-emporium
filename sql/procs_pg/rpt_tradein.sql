/* =============================================================
   rpt — trade-in reports (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TradeInSummary(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT extract(year from occurred_at) AS yr, extract(month from occurred_at) AS mo,
           COUNT(*) AS items,
           SUM(CASE WHEN is_hardware THEN 1 ELSE 0 END) AS hardware_items,
           CAST(SUM(offer_amount_usd) AS decimal(16,2)) AS offered_usd
    FROM dw.fact_tradein
    WHERE (p_year IS NULL OR extract(year from occurred_at) = p_year)
    GROUP BY extract(year from occurred_at), extract(month from occurred_at)
    ORDER BY yr, mo;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TradeInByPlatform(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT COALESCE(p.platform_name, '(unknown)') AS platform_name,
           COUNT(*) AS items, CAST(SUM(t.offer_amount_usd) AS decimal(16,2)) AS offered_usd
    FROM dw.fact_tradein t
    LEFT JOIN dw.dim_product p ON p.product_key = t.product_key
    WHERE (p_year IS NULL OR extract(year from t.occurred_at) = p_year)
    GROUP BY p.platform_name ORDER BY offered_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TradeInTopProducts(p_top int DEFAULT 25)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.title, p.platform_name, p.product_kind,
           COUNT(*) AS times_traded, CAST(AVG(t.offer_amount_usd) AS decimal(12,2)) AS avg_offer_usd
    FROM dw.fact_tradein t
    JOIN dw.dim_product p ON p.product_key = t.product_key
    GROUP BY p.title, p.platform_name, p.product_kind
    ORDER BY times_traded DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_PayoutMethodMix(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT tr.payout_method,
           COUNT(*) AS trade_ins,
           CAST(SUM(tr.total_value / COALESCE(NULLIF(fx.rate_to_usd,0),1.0)) AS decimal(16,2)) AS value_usd
    FROM dbo.trade_ins tr
    LEFT JOIN dw.dim_fx fx ON fx.currency_code = tr.currency_code AND fx.year = extract(year from tr.occurred_at)::smallint
    WHERE (p_year IS NULL OR extract(year from tr.occurred_at) = p_year)
    GROUP BY tr.payout_method ORDER BY value_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TradeInAttachToSale(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    -- what share of trade-ins were blended with a purchase (upgrade cycle)
    SELECT extract(year from occurred_at) AS yr,
           COUNT(*) AS trade_ins,
           SUM(CASE WHEN transaction_id IS NOT NULL THEN 1 ELSE 0 END) AS with_purchase,
           CAST(100.0*SUM(CASE WHEN transaction_id IS NOT NULL THEN 1 ELSE 0 END)/NULLIF(COUNT(*),0) AS decimal(5,1)) AS attach_pct
    FROM dbo.trade_ins
    WHERE (p_year IS NULL OR extract(year from occurred_at) = p_year)
    GROUP BY extract(year from occurred_at) ORDER BY yr;
    RETURN c;
END $$;
