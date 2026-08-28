/* =============================================================
   dw.dim_fx — dense FX dimension (PostgreSQL port of 15_dim_fx.sql)
   -------------------------------------------------------------
   public.fx_rates is a 5-year snapshot (1990,1995,...). dim_fx densifies
   it to one row per (currency, year) carrying the era-correct rate =
   the latest snapshot at or before that year (earliest-snapshot
   fallback for pre-1990) — the same rule finance.monthly_summary uses.
   Facts join on (currency, year): a small exact join, no per-row
   correlated lookup over billions. rate_to_usd = local units per 1 USD
   (divide to normalise).
   ============================================================= */
CREATE TABLE IF NOT EXISTS dw.dim_fx (
    currency_code CHAR(3)       NOT NULL,
    year          SMALLINT      NOT NULL,
    rate_to_usd   DECIMAL(14,6) NOT NULL,   -- local units per 1 USD -> divide
    CONSTRAINT pk_dim_fx PRIMARY KEY (currency_code, year)
);
