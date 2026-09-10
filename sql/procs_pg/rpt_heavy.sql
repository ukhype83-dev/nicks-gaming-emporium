/* =============================================================
   rpt — heavy analytical reports (PostgreSQL port; genuinely slow
   by design — the slowness is inherent, not an anti-pattern).
   ============================================================= */

/* External-auditor reconciliation: re-scans the sales ledger @passes times. */
CREATE OR REPLACE FUNCTION rpt.usp_rpt_AnnualSalesAudit(p_passes int DEFAULT 3)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; v_i int := 0; v_gross numeric(38,2); v_foot numeric(38,2) := 0;
BEGIN
    WHILE v_i < p_passes LOOP
        SELECT SUM(line_total * 1.000001) INTO v_gross FROM dw.fact_sales WHERE line_total > v_i;  -- full scan each pass
        v_foot := v_foot + COALESCE(v_gross,0);
        v_i := v_i + 1;
    END LOOP;
    OPEN c FOR SELECT p_passes AS passes, v_foot AS cross_foot_control;
    RETURN c;
END $$;

/* Finance's full sales-ledger export, sorted by value (large unsupported sort). */
CREATE OR REPLACE FUNCTION rpt.usp_rpt_FullLedgerExport(p_rows int DEFAULT 2000000)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT transaction_line_id, occurred_at, product_key, customer_key, line_total_usd
    FROM dw.fact_sales
    ORDER BY line_total_usd DESC, customer_key, occurred_at
    LIMIT p_rows;
    RETURN c;
END $$;

/* Shop-similarity matrix: a cross join of every shop with every other shop. */
CREATE OR REPLACE FUNCTION rpt.usp_rpt_ShopSimilarityMatrix(p_top int DEFAULT 200)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT a.shop_key AS shop_a, b.shop_key AS shop_b,
           a.country_code, ABS(b.opened_date - a.opened_date) AS open_gap_days
    FROM dw.dim_shop a
    CROSS JOIN dw.dim_shop b                                     -- every shop x every shop
    WHERE a.shop_key < b.shop_key AND a.country_code = b.country_code
    ORDER BY a.shop_key, open_gap_days
    LIMIT p_top;
    RETURN c;
END $$;
