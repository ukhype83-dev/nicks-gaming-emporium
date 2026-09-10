/* =============================================================
   rpt — finance analytics, part 2 (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_CashFlowProxy(p_from_ym char(7) DEFAULT '2008-01', p_to_ym char(7) DEFAULT '2016-09')
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT year_month,
           revenue_usd,
           (cogs_usd + wages_usd + severance_usd + rent_usd + other_opex_usd) AS total_costs_usd,
           revenue_usd - (cogs_usd + wages_usd + severance_usd + rent_usd + other_opex_usd) AS operating_cf_usd
    FROM finance.monthly_summary
    WHERE year_month BETWEEN p_from_ym AND p_to_ym ORDER BY year_month;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_CostBreakdown(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT LEFT(year_month,4) AS yr,
           SUM(cogs_usd) AS cogs, SUM(wages_usd) AS wages, SUM(severance_usd) AS severance,
           SUM(rent_usd) AS rent, SUM(other_opex_usd) AS other_opex
    FROM finance.monthly_summary
    WHERE (p_year IS NULL OR LEFT(year_month,4)=p_year::text)
    GROUP BY LEFT(year_month,4) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_CurrencyExposure(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT f.currency_code,
           COUNT(*) AS lines,
           SUM(f.line_total_usd) AS revenue_usd,
           CAST(100.0*SUM(f.line_total_usd)/SUM(SUM(f.line_total_usd)) OVER () AS decimal(5,2)) AS pct_of_usd
    FROM dw.fact_sales f
    WHERE NOT f.is_return AND (p_year IS NULL OR extract(year from f.occurred_at)=p_year)
    GROUP BY f.currency_code ORDER BY revenue_usd DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ProfitableMonths()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT year_month, net_income_usd,
           CASE WHEN net_income_usd >= 0 THEN 'profit' ELSE 'loss' END AS result
    FROM finance.monthly_summary
    ORDER BY year_month;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TaxCollected(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT extract(year from f.occurred_at) AS yr,
           CAST(SUM(f.line_tax / COALESCE(NULLIF(fx.rate_to_usd,0),1.0)) AS decimal(16,2)) AS tax_usd
    FROM dw.fact_sales f
    LEFT JOIN dw.dim_fx fx ON fx.currency_code=f.currency_code AND fx.year=extract(year from f.occurred_at)::smallint
    WHERE NOT f.is_return AND (p_year IS NULL OR extract(year from f.occurred_at)=p_year)
    GROUP BY extract(year from f.occurred_at) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_StoreContribution(p_year int DEFAULT 2008, p_top int DEFAULT 25)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT s.shop_key, s.name, s.country_code,
           SUM(a.revenue_usd) AS revenue_usd, SUM(a.tx_count) AS transactions
    FROM dw.agg_sales_by_day_shop a
    JOIN dw.dim_shop s ON s.shop_key = a.shop_key
    WHERE a.date_key BETWEEN p_year*10000 AND p_year*10000+1231
    GROUP BY s.shop_key, s.name, s.country_code
    ORDER BY revenue_usd DESC
    LIMIT p_top;
    RETURN c;
END $$;
