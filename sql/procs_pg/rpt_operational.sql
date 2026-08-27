/* =============================================================
   rpt — operational / day-to-day reports (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_DailyClose(p_date date)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; v_dk int := to_char(p_date,'YYYYMMDD')::int;
BEGIN
    OPEN c FOR
    SELECT SUM(a.tx_count) AS transactions, SUM(a.units) AS units,
           SUM(a.revenue_usd) AS revenue_usd, COUNT(DISTINCT a.shop_key) AS shops_trading
    FROM dw.agg_sales_by_day_shop a WHERE a.date_key = v_dk;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ShopDayReport(p_shop_key bigint, p_date date)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT ch.channel_name,
           COUNT(DISTINCT f.transaction_id) AS transactions,
           SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f
    JOIN dw.dim_channel ch ON ch.channel_key = f.channel_key
    WHERE f.shop_key = p_shop_key AND f.occurred_at::date = p_date
    GROUP BY ch.channel_name ORDER BY revenue_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_SalesByHour(p_date date)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT extract(hour from occurred_at) AS hr,
           COUNT(*) AS lines, SUM(line_total_usd) AS revenue_usd
    FROM dw.fact_sales
    WHERE occurred_at::date = p_date
    GROUP BY extract(hour from occurred_at) ORDER BY hr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TopSellersForDay(p_date date, p_top int DEFAULT 20)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.title, p.platform_name, SUM(f.quantity) AS units, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE f.occurred_at::date = p_date AND NOT f.is_return
    GROUP BY p.title, p.platform_name ORDER BY units DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_RefundsForDay(p_date date)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.title, p.platform_name, COUNT(*) AS refund_lines, SUM(-f.line_total_usd) AS refund_usd
    FROM dw.fact_sales f JOIN dw.dim_product p ON p.product_key = f.product_key
    WHERE f.occurred_at::date = p_date AND f.is_return
    GROUP BY p.title, p.platform_name ORDER BY refund_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ReturnsRate(p_from_ym char(7), p_to_ym char(7))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT year_month,
           gross_revenue_usd, return_count,
           CAST(-return_value_usd AS decimal(16,2)) AS refunds_usd,
           CAST(100.0*(-return_value_usd)/NULLIF(gross_revenue_usd,0) AS decimal(5,2)) AS return_pct
    FROM dw.agg_sales_by_month WHERE year_month BETWEEN p_from_ym AND p_to_ym ORDER BY year_month;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_NewCustomersForDay(p_date date)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT c.country_code, COUNT(*) AS new_customers
    FROM dw.dim_customer c WHERE c.signup_date = p_date
    GROUP BY c.country_code ORDER BY new_customers DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ChannelSalesForDay(p_date date)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT ch.channel_name, COUNT(DISTINCT f.transaction_id) AS transactions, SUM(f.line_total_usd) AS revenue_usd
    FROM dw.fact_sales f JOIN dw.dim_channel ch ON ch.channel_key = f.channel_key
    WHERE f.occurred_at::date = p_date
    GROUP BY ch.channel_name ORDER BY revenue_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_WeekInReview(p_week_ending date)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT occurred_at::date AS day, COUNT(*) AS lines, SUM(line_total_usd) AS revenue_usd
    FROM dw.fact_sales
    WHERE occurred_at >= (p_week_ending - 6) AND occurred_at < (p_week_ending + 1)
    GROUP BY occurred_at::date ORDER BY day;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ShopRankForDay(p_date date, p_top int DEFAULT 25)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; v_dk int := to_char(p_date,'YYYYMMDD')::int;
BEGIN
    OPEN c FOR
    SELECT s.name, s.country_code, a.tx_count, a.units, a.revenue_usd
    FROM dw.agg_sales_by_day_shop a JOIN dw.dim_shop s ON s.shop_key = a.shop_key
    WHERE a.date_key = v_dk ORDER BY a.revenue_usd DESC
    LIMIT p_top;
    RETURN c;
END $$;
