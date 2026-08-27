// V1.15.0 — post-load aggregation that fills finance.monthly_summary.
//
// Runs once after all retail / hr data is loaded. Tally table of every
// month from 1986-08 to 2016-09 (NGE's operational lifespan); per
// month, computes:
//
//   - revenue_usd  : sum of dbo.transactions.total in shop's currency,
//                    FX-converted to USD with ERA-CORRECT rates
//                    (V1.18.0): for each calendar year, the latest
//                    dbo.fx_rates five-year snapshot at or before that
//                    year, falling back to the earliest snapshot for
//                    pre-1990 months — the same policy as dbo.fn_ToUSD
//                    and the training catalog's "faithful rewrite", so
//                    detail-vs-finance reconciliation closes to within
//                    FX-snapshot granularity (target ±2%/year). Note
//                    rate_to_usd is LOCAL UNITS PER USD — divide.
//                    (Pre-V1.18 this used one hardcoded MODERN rate
//                    table for all of 1986-2016 — the root cause of
//                    the ~13% reconciliation gap, audit finding F5e.)
//                    Revenue stays GROSS OF TAX and rent stays a flat
//                    $3,000/shop/month — deliberate definitional
//                    choices documented here so nobody "fixes" them
//                    and silently re-calibrates the V1.15.2 ratios.
//   - cogs_usd     : revenue × a MIX-DRIVEN cost ratio (V1.20). Pre-V1.20
//                    this was a flat 70% for 30 years — a synthetic-P&L
//                    tell that also contradicted the product mix. The
//                    ratio now declines as the high-margin used-game share
//                    grows: ~0.72 in the new-dominant 1980s/90s (≈28%
//                    gross margin), easing to ~0.61 by 2016 (≈39% margin)
//                    — matching real specialty-games retail (GameStop-class
//                    margins improved with pre-owned). The 2016 fire-sale
//                    quarters (Apr-Sep) override to 0.92 as clearance
//                    pricing collapses margins. Smooth (not stepped) so it
//                    doesn't read as robotic. Derived purely from the
//                    calendar — recomputable in place from stored
//                    revenue_usd, no transaction re-scan.
//   - wages_usd    : sum of compensation_history records active in the
//                    month (annual_wage / 12), FX-converted to USD, PLUS
//                    (V1.19) the whole-amount annual 'bonus' lump landing
//                    in that month — so wages-of-revenue reflects total
//                    comp. Bonuses show as a March spike each profitable
//                    year (1996-2010 full, tapering to zero by 2013).
//   - severance_usd: lumps from compensation_history with change_reason
//                    LIKE 'severance_%' OR 'winddown_%'
//   - rent_usd     : V1.22.0 — $3,000/shop/month base × per-country
//                    occupancy factor (Tokyo/London/Zurich dear, Brazil/
//                    Poland cheap) × era inflation (0.45 in 1986 → 1.05 in
//                    2016). Replaces the pre-V1.22 flat $3,000-everywhere
//                    proxy (a synthetic tell). See rent_for_month CTE.
//   - other_opex_usd: revenue × an era-varying rate (V1.22.0: 5% ≤2000,
//                    6% 2001-2010, 8% in the 2011-2016 decline as the
//                    company spent into the collapse) — catch-all
//                    for marketing (1-3%),
//                    utilities/energy (1-2%), IT (1-2%), freight (2-5%),
//                    insurance, merchant fees, inventory shrink,
//                    depreciation. Brings peak operating margin in line
//                    with real specialty-retail benchmarks (Best Buy /
//                    GameStop ~4-7%) rather than software-margin levels.
//   - shops_active : count of shops where opened_date <= month_end AND
//                    (closed_date IS NULL OR closed_date >= month_start)
//   - staff_active : equivalent count of employment_spells
//   - net_income_usd = revenue - cogs - wages - severance - rent - other_opex
//
// Single big SQL statement, server-side, ~1-2 seconds on 300g.

package dbwriter

import (
	"context"
	"fmt"
)

func populateMonthlySummary(ctx context.Context, w Writer, s *LoadAllStats, progress func(string, int64)) error {
	existing, err := w.MaxBigint(ctx, "finance", "monthly_summary", "shops_active")
	if err != nil {
		return err
	}
	if existing > 0 {
		// Already populated by a prior run.
		return nil
	}

	stmtMSSQL := `
;WITH months(month_start) AS (
    SELECT CONVERT(date, '1986-08-01')
    UNION ALL
    SELECT DATEADD(month, 1, month_start) FROM months WHERE month_start < '2016-09-01'
),
year_fx AS (
    /* V1.18.0 — era-correct FX: per (year, currency), the latest
       dbo.fx_rates snapshot at or before that year; earliest-snapshot
       fallback for pre-1990. rate_to_usd = local units per USD. */
    SELECT y.yr, c.currency_code,
           COALESCE(
             (SELECT TOP 1 f.rate_to_usd FROM dbo.fx_rates f
              WHERE f.currency_code = c.currency_code
                AND f.effective_year <= y.yr
              ORDER BY f.effective_year DESC),
             (SELECT TOP 1 f.rate_to_usd FROM dbo.fx_rates f
              WHERE f.currency_code = c.currency_code
              ORDER BY f.effective_year ASC)) AS rate_to_usd
    FROM (SELECT DISTINCT YEAR(month_start) AS yr FROM months) y
    CROSS JOIN dbo.currencies c
),
shops_for_month AS (
    SELECT m.month_start,
           COUNT(*) AS shops_active
    FROM months m
    LEFT JOIN dbo.shops s
      ON s.opened_date <= EOMONTH(m.month_start)
     AND (s.closed_date IS NULL OR s.closed_date >= m.month_start)
    GROUP BY m.month_start
),
rent_for_month AS (
    /* V1.22.0 — rent is no longer a flat $3,000/shop. It scales by a
       per-country occupancy factor (Tokyo/London/Zurich dear, Brazil/
       Poland cheap) and by era inflation (a 1986 lease cost ~45% of a
       2016 one in nominal USD). Replaces the synthetic flat-rent tell. */
    SELECT m.month_start,
           ISNULL(SUM(
             3000.0
             * CASE s.country_code
                 WHEN 'US' THEN 1.00 WHEN 'GB' THEN 1.15 WHEN 'JP' THEN 1.40
                 WHEN 'CH' THEN 1.30 WHEN 'NO' THEN 1.25 WHEN 'DK' THEN 1.15
                 WHEN 'SE' THEN 1.10 WHEN 'DE' THEN 1.05 WHEN 'FR' THEN 1.05
                 WHEN 'NL' THEN 1.05 WHEN 'AU' THEN 1.05 WHEN 'IT' THEN 0.95
                 WHEN 'CA' THEN 0.95 WHEN 'KR' THEN 0.95 WHEN 'ES' THEN 0.90
                 WHEN 'BR' THEN 0.65 WHEN 'PL' THEN 0.55 WHEN 'CZ' THEN 0.55
                 ELSE 0.90
               END
             * (0.45 + 0.020 * (YEAR(m.month_start) - 1986)) -- 0.45 (1986) → 1.05 (2016)
           ), 0) AS rent_usd
    FROM months m
    LEFT JOIN dbo.shops s
      ON s.opened_date <= EOMONTH(m.month_start)
     AND (s.closed_date IS NULL OR s.closed_date >= m.month_start)
    GROUP BY m.month_start
),
staff_for_month AS (
    SELECT m.month_start,
           COUNT(*) AS staff_active
    FROM months m
    LEFT JOIN hr.employment_spells es
      ON es.started_at <= EOMONTH(m.month_start)
     AND (es.ended_at IS NULL OR es.ended_at >= m.month_start)
    GROUP BY m.month_start
),
revenue_local AS (
    /* pre-aggregate in local currency BEFORE the FX join — keeps the
       big-table pass cheap (no per-row FX lookup over billions). */
    SELECT
      DATEFROMPARTS(YEAR(t.occurred_at), MONTH(t.occurred_at), 1) AS month_start,
      s.currency_code,
      SUM(t.total) AS total_local
    FROM dbo.transactions t
    JOIN dbo.shops s ON s.shop_id = t.shop_id
    GROUP BY DATEFROMPARTS(YEAR(t.occurred_at), MONTH(t.occurred_at), 1),
             s.currency_code
),
revenue_for_month AS (
    SELECT rl.month_start,
           SUM(rl.total_local / ISNULL(fx.rate_to_usd, 1.0)) AS revenue_usd
    FROM revenue_local rl
    LEFT JOIN year_fx fx ON fx.yr = YEAR(rl.month_start)
                        AND fx.currency_code = rl.currency_code
    GROUP BY rl.month_start
),
wages_for_month AS (
    -- A compensation record is "active" if effective_from <= month_end
    -- AND (effective_to IS NULL OR effective_to >= month_start). Sum
    -- annual_wage / 12 over active recurring records, FX-converted via
    -- the person's country (joined through persons → currency_code).
    -- One-off severance rows are excluded here (summed in
    -- severance_for_month). V1.19: 'bonus' rows are also excluded here
    -- (they are single-day lumps, not recurring monthly wage) and summed
    -- as a whole-amount lump in bonus_for_month.
    SELECT
      m.month_start,
      ISNULL(SUM(c.annual_wage / 12.0 / ISNULL(fx.rate_to_usd, 1.0)), 0) AS wages_usd
    FROM months m
    LEFT JOIN hr.compensation_history c
      ON c.effective_from <= EOMONTH(m.month_start)
     AND (c.effective_to IS NULL OR c.effective_to >= m.month_start)
     AND c.change_reason NOT LIKE 'severance_%'
     AND c.change_reason <> 'bonus'
    LEFT JOIN year_fx fx ON fx.yr = YEAR(m.month_start)
                        AND fx.currency_code = c.currency_code
    GROUP BY m.month_start
),
severance_for_month AS (
    SELECT
      DATEFROMPARTS(YEAR(c.effective_from), MONTH(c.effective_from), 1) AS month_start,
      SUM(c.annual_wage / ISNULL(fx.rate_to_usd, 1.0)) AS severance_usd
    FROM hr.compensation_history c
    LEFT JOIN year_fx fx ON fx.yr = YEAR(c.effective_from)
                        AND fx.currency_code = c.currency_code
    WHERE c.change_reason LIKE 'severance_%'
    GROUP BY DATEFROMPARTS(YEAR(c.effective_from), MONTH(c.effective_from), 1)
),
bonus_for_month AS (
    -- V1.19 — annual performance bonus paid as a single-day lump (the
    -- WHOLE amount, not /12) into the month of effective_from. Folded
    -- into wages_usd below so wages-of-revenue reflects total comp.
    SELECT
      DATEFROMPARTS(YEAR(c.effective_from), MONTH(c.effective_from), 1) AS month_start,
      SUM(c.annual_wage / ISNULL(fx.rate_to_usd, 1.0)) AS bonus_usd
    FROM hr.compensation_history c
    LEFT JOIN year_fx fx ON fx.yr = YEAR(c.effective_from)
                        AND fx.currency_code = c.currency_code
    WHERE c.change_reason = 'bonus'
    GROUP BY DATEFROMPARTS(YEAR(c.effective_from), MONTH(c.effective_from), 1)
),
cogs_ratio AS (
    -- V1.21.2 — HONEST three-way blended cost ratio by the per-month
    -- revenue split: SOFTWARE (the V1.20 calendar curve ~0.72→0.61, which
    -- already reflects the rising used-game mix), CONSOLE hardware (~0.95
    -- razor-and-blade, sold near cost), and ACCESSORIES (~0.65, the high-
    -- margin peripherals — the profit "blade"). Console kind ∈ {console,
    -- handheld, computer}; accessory kind = 'accessory'. Replaces the
    -- V1.21.1 single 0.85 hardware hand-wave with the real product mix.
    -- Fire-sale quarters (Apr-Sep 2016) override to 0.92. revenue_usd
    -- itself is unchanged (SUM(transactions.total)).
    SELECT hf.month_start,
           CASE
             WHEN YEAR(hf.month_start) = 2016 AND MONTH(hf.month_start) BETWEEN 4 AND 9 THEN 0.92
             ELSE (1.0 - hf.console_frac - hf.acc_frac) * (
                    CASE
                      WHEN YEAR(hf.month_start) <= 1998 THEN 0.72
                      WHEN YEAR(hf.month_start) <= 2007 THEN 0.72 - 0.005 * (YEAR(hf.month_start) - 1998)
                      ELSE 0.675 - 0.007 * (YEAR(hf.month_start) - 2007)
                    END)
                  + hf.console_frac * 0.93
                  + hf.acc_frac * 0.65
           END AS ratio
    FROM (
        SELECT DATEFROMPARTS(YEAR(t.occurred_at), MONTH(t.occurred_at), 1) AS month_start,
               CAST(SUM(CASE WHEN h.hardware_id IS NOT NULL AND h.kind <> 'accessory' THEN tl.line_total ELSE 0 END) AS FLOAT)
                 / NULLIF(SUM(tl.line_total), 0) AS console_frac,
               CAST(SUM(CASE WHEN h.kind = 'accessory' THEN tl.line_total ELSE 0 END) AS FLOAT)
                 / NULLIF(SUM(tl.line_total), 0) AS acc_frac
        FROM dbo.transaction_lines tl
        JOIN dbo.transactions t ON t.transaction_id = tl.transaction_id
        LEFT JOIN dbo.hardware h ON h.hardware_id = tl.hardware_id
        GROUP BY DATEFROMPARTS(YEAR(t.occurred_at), MONTH(t.occurred_at), 1)
    ) hf
)
INSERT INTO finance.monthly_summary (
    year_month, revenue_usd, cogs_usd, wages_usd, severance_usd,
    rent_usd, other_opex_usd, net_income_usd,
    shops_active, staff_active, notes)
SELECT
    FORMAT(m.month_start, 'yyyy-MM') AS year_month,
    ISNULL(rev.revenue_usd, 0)                                                   AS revenue_usd,
    ISNULL(rev.revenue_usd, 0) * ISNULL(cr.ratio, 0.70)                          AS cogs_usd,
    ISNULL(w.wages_usd, 0) + ISNULL(bon.bonus_usd, 0)                            AS wages_usd,
    ISNULL(sev.severance_usd, 0)                                                 AS severance_usd,
    ISNULL(rfm.rent_usd, 0)                                                       AS rent_usd,
    ISNULL(rev.revenue_usd, 0) * (CASE
        WHEN YEAR(m.month_start) <= 2000 THEN 0.05
        WHEN YEAR(m.month_start) <= 2010 THEN 0.06
        ELSE 0.08 END)                                                           AS other_opex_usd,
    ISNULL(rev.revenue_usd, 0)
        - ISNULL(rev.revenue_usd, 0) * ISNULL(cr.ratio, 0.70)
        - ISNULL(w.wages_usd, 0)
        - ISNULL(bon.bonus_usd, 0)
        - ISNULL(sev.severance_usd, 0)
        - ISNULL(rfm.rent_usd, 0)
        - ISNULL(rev.revenue_usd, 0) * (CASE
            WHEN YEAR(m.month_start) <= 2000 THEN 0.05
            WHEN YEAR(m.month_start) <= 2010 THEN 0.06
            ELSE 0.08 END)                                                       AS net_income_usd,
    ISNULL(sfm.shops_active, 0)                                                  AS shops_active,
    ISNULL(stm.staff_active, 0)                                                  AS staff_active,
    NULL                                                                          AS notes
FROM months m
LEFT JOIN shops_for_month     sfm ON sfm.month_start = m.month_start
LEFT JOIN rent_for_month      rfm ON rfm.month_start = m.month_start
LEFT JOIN staff_for_month     stm ON stm.month_start = m.month_start
LEFT JOIN revenue_for_month   rev ON rev.month_start = m.month_start
LEFT JOIN cogs_ratio          cr  ON cr.month_start  = m.month_start
LEFT JOIN wages_for_month     w   ON w.month_start   = m.month_start
LEFT JOIN bonus_for_month     bon ON bon.month_start = m.month_start
LEFT JOIN severance_for_month sev ON sev.month_start = m.month_start
ORDER BY m.month_start
OPTION (MAXRECURSION 400);
`

	// PostgreSQL port of the same aggregation. Identical shape and outputs;
	// the T-SQL-isms are swapped: generate_series for the month spine (no
	// recursive CTE), LIMIT for TOP, EXTRACT for YEAR/MONTH, date_trunc +
	// interval for EOMONTH/DATEFROMPARTS/DATEADD, COALESCE for ISNULL,
	// to_char for FORMAT, double precision for FLOAT.
	stmtPostgres := `
WITH months(month_start) AS (
    SELECT generate_series(DATE '1986-08-01', DATE '2016-09-01', INTERVAL '1 month')::date
),
year_fx AS (
    SELECT y.yr, c.currency_code,
           COALESCE(
             (SELECT f.rate_to_usd FROM dbo.fx_rates f
              WHERE f.currency_code = c.currency_code AND f.effective_year <= y.yr
              ORDER BY f.effective_year DESC LIMIT 1),
             (SELECT f.rate_to_usd FROM dbo.fx_rates f
              WHERE f.currency_code = c.currency_code
              ORDER BY f.effective_year ASC LIMIT 1)) AS rate_to_usd
    FROM (SELECT DISTINCT EXTRACT(YEAR FROM month_start)::int AS yr FROM months) y
    CROSS JOIN dbo.currencies c
),
shops_for_month AS (
    SELECT m.month_start, COUNT(*) AS shops_active
    FROM months m
    LEFT JOIN dbo.shops s
      ON s.opened_date <= (date_trunc('month', m.month_start) + INTERVAL '1 month' - INTERVAL '1 day')::date
     AND (s.closed_date IS NULL OR s.closed_date >= m.month_start)
    GROUP BY m.month_start
),
rent_for_month AS (
    SELECT m.month_start,
           COALESCE(SUM(
             3000.0
             * CASE s.country_code
                 WHEN 'US' THEN 1.00 WHEN 'GB' THEN 1.15 WHEN 'JP' THEN 1.40
                 WHEN 'CH' THEN 1.30 WHEN 'NO' THEN 1.25 WHEN 'DK' THEN 1.15
                 WHEN 'SE' THEN 1.10 WHEN 'DE' THEN 1.05 WHEN 'FR' THEN 1.05
                 WHEN 'NL' THEN 1.05 WHEN 'AU' THEN 1.05 WHEN 'IT' THEN 0.95
                 WHEN 'CA' THEN 0.95 WHEN 'KR' THEN 0.95 WHEN 'ES' THEN 0.90
                 WHEN 'BR' THEN 0.65 WHEN 'PL' THEN 0.55 WHEN 'CZ' THEN 0.55
                 ELSE 0.90
               END
             * (0.45 + 0.020 * (EXTRACT(YEAR FROM m.month_start)::int - 1986))
           ), 0) AS rent_usd
    FROM months m
    LEFT JOIN dbo.shops s
      ON s.opened_date <= (date_trunc('month', m.month_start) + INTERVAL '1 month' - INTERVAL '1 day')::date
     AND (s.closed_date IS NULL OR s.closed_date >= m.month_start)
    GROUP BY m.month_start
),
staff_for_month AS (
    SELECT m.month_start, COUNT(*) AS staff_active
    FROM months m
    LEFT JOIN hr.employment_spells es
      ON es.started_at <= (date_trunc('month', m.month_start) + INTERVAL '1 month' - INTERVAL '1 day')::date
     AND (es.ended_at IS NULL OR es.ended_at >= m.month_start)
    GROUP BY m.month_start
),
revenue_local AS (
    SELECT date_trunc('month', t.occurred_at)::date AS month_start,
           s.currency_code,
           SUM(t.total) AS total_local
    FROM dbo.transactions t
    JOIN dbo.shops s ON s.shop_id = t.shop_id
    GROUP BY date_trunc('month', t.occurred_at)::date, s.currency_code
),
revenue_for_month AS (
    SELECT rl.month_start,
           SUM(rl.total_local / COALESCE(fx.rate_to_usd, 1.0)) AS revenue_usd
    FROM revenue_local rl
    LEFT JOIN year_fx fx ON fx.yr = EXTRACT(YEAR FROM rl.month_start)::int
                        AND fx.currency_code = rl.currency_code
    GROUP BY rl.month_start
),
wages_for_month AS (
    SELECT m.month_start,
           COALESCE(SUM(c.annual_wage / 12.0 / COALESCE(fx.rate_to_usd, 1.0)), 0) AS wages_usd
    FROM months m
    LEFT JOIN hr.compensation_history c
      ON c.effective_from <= (date_trunc('month', m.month_start) + INTERVAL '1 month' - INTERVAL '1 day')::date
     AND (c.effective_to IS NULL OR c.effective_to >= m.month_start)
     AND c.change_reason NOT LIKE 'severance_%'
     AND c.change_reason <> 'bonus'
    LEFT JOIN year_fx fx ON fx.yr = EXTRACT(YEAR FROM m.month_start)::int
                        AND fx.currency_code = c.currency_code
    GROUP BY m.month_start
),
severance_for_month AS (
    SELECT date_trunc('month', c.effective_from)::date AS month_start,
           SUM(c.annual_wage / COALESCE(fx.rate_to_usd, 1.0)) AS severance_usd
    FROM hr.compensation_history c
    LEFT JOIN year_fx fx ON fx.yr = EXTRACT(YEAR FROM c.effective_from)::int
                        AND fx.currency_code = c.currency_code
    WHERE c.change_reason LIKE 'severance_%'
    GROUP BY date_trunc('month', c.effective_from)::date
),
bonus_for_month AS (
    SELECT date_trunc('month', c.effective_from)::date AS month_start,
           SUM(c.annual_wage / COALESCE(fx.rate_to_usd, 1.0)) AS bonus_usd
    FROM hr.compensation_history c
    LEFT JOIN year_fx fx ON fx.yr = EXTRACT(YEAR FROM c.effective_from)::int
                        AND fx.currency_code = c.currency_code
    WHERE c.change_reason = 'bonus'
    GROUP BY date_trunc('month', c.effective_from)::date
),
cogs_ratio AS (
    SELECT hf.month_start,
           CASE
             WHEN EXTRACT(YEAR FROM hf.month_start)::int = 2016 AND EXTRACT(MONTH FROM hf.month_start)::int BETWEEN 4 AND 9 THEN 0.92
             ELSE (1.0 - hf.console_frac - hf.acc_frac) * (
                    CASE
                      WHEN EXTRACT(YEAR FROM hf.month_start)::int <= 1998 THEN 0.72
                      WHEN EXTRACT(YEAR FROM hf.month_start)::int <= 2007 THEN 0.72 - 0.005 * (EXTRACT(YEAR FROM hf.month_start)::int - 1998)
                      ELSE 0.675 - 0.007 * (EXTRACT(YEAR FROM hf.month_start)::int - 2007)
                    END)
                  + hf.console_frac * 0.93
                  + hf.acc_frac * 0.65
           END AS ratio
    FROM (
        SELECT date_trunc('month', t.occurred_at)::date AS month_start,
               CAST(SUM(CASE WHEN h.hardware_id IS NOT NULL AND h.kind <> 'accessory' THEN tl.line_total ELSE 0 END) AS double precision)
                 / NULLIF(SUM(tl.line_total), 0) AS console_frac,
               CAST(SUM(CASE WHEN h.kind = 'accessory' THEN tl.line_total ELSE 0 END) AS double precision)
                 / NULLIF(SUM(tl.line_total), 0) AS acc_frac
        FROM dbo.transaction_lines tl
        JOIN dbo.transactions t ON t.transaction_id = tl.transaction_id
        LEFT JOIN dbo.hardware h ON h.hardware_id = tl.hardware_id
        GROUP BY date_trunc('month', t.occurred_at)::date
    ) hf
)
INSERT INTO finance.monthly_summary (
    year_month, revenue_usd, cogs_usd, wages_usd, severance_usd,
    rent_usd, other_opex_usd, net_income_usd,
    shops_active, staff_active, notes)
SELECT
    to_char(m.month_start, 'YYYY-MM') AS year_month,
    COALESCE(rev.revenue_usd, 0)                                                 AS revenue_usd,
    COALESCE(rev.revenue_usd, 0) * COALESCE(cr.ratio, 0.70)                       AS cogs_usd,
    COALESCE(w.wages_usd, 0) + COALESCE(bon.bonus_usd, 0)                         AS wages_usd,
    COALESCE(sev.severance_usd, 0)                                               AS severance_usd,
    COALESCE(rfm.rent_usd, 0)                                                    AS rent_usd,
    COALESCE(rev.revenue_usd, 0) * (CASE
        WHEN EXTRACT(YEAR FROM m.month_start)::int <= 2000 THEN 0.05
        WHEN EXTRACT(YEAR FROM m.month_start)::int <= 2010 THEN 0.06
        ELSE 0.08 END)                                                          AS other_opex_usd,
    COALESCE(rev.revenue_usd, 0)
        - COALESCE(rev.revenue_usd, 0) * COALESCE(cr.ratio, 0.70)
        - COALESCE(w.wages_usd, 0)
        - COALESCE(bon.bonus_usd, 0)
        - COALESCE(sev.severance_usd, 0)
        - COALESCE(rfm.rent_usd, 0)
        - COALESCE(rev.revenue_usd, 0) * (CASE
            WHEN EXTRACT(YEAR FROM m.month_start)::int <= 2000 THEN 0.05
            WHEN EXTRACT(YEAR FROM m.month_start)::int <= 2010 THEN 0.06
            ELSE 0.08 END)                                                      AS net_income_usd,
    COALESCE(sfm.shops_active, 0)                                               AS shops_active,
    COALESCE(stm.staff_active, 0)                                               AS staff_active,
    NULL                                                                        AS notes
FROM months m
LEFT JOIN shops_for_month     sfm ON sfm.month_start = m.month_start
LEFT JOIN rent_for_month      rfm ON rfm.month_start = m.month_start
LEFT JOIN staff_for_month     stm ON stm.month_start = m.month_start
LEFT JOIN revenue_for_month   rev ON rev.month_start = m.month_start
LEFT JOIN cogs_ratio          cr  ON cr.month_start  = m.month_start
LEFT JOIN wages_for_month     w   ON w.month_start   = m.month_start
LEFT JOIN bonus_for_month     bon ON bon.month_start = m.month_start
LEFT JOIN severance_for_month sev ON sev.month_start = m.month_start
ORDER BY m.month_start;
`

	stmt := stmtMSSQL
	if _, ok := w.(*Postgres); ok {
		stmt = stmtPostgres
	}
	if err := w.ExecSQL(ctx, stmt); err != nil {
		return fmt.Errorf("monthly_summary aggregation: %w", err)
	}

	// Count what we inserted (cheap MAX on count).
	n, err := w.MaxBigint(ctx, "finance", "monthly_summary", "shops_active")
	if err == nil && n > 0 {
		// shops_active max isn't row-count, but a non-zero return means rows exist.
		// Get actual count via a different signal — use staff_active max as a proxy.
		s.MonthlySummary = 1 // signal "populated"; refine in stats later if needed
	}
	progress("finance.monthly_summary", s.MonthlySummary)
	return nil
}
