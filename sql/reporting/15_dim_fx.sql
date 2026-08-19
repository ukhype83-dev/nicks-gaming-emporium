-- dw.dim_fx: dense FX dimension — one row per (currency, year) carrying the
-- rate = latest dbo.fx_rates snapshot at or before that year (earliest-snapshot
-- fallback pre-1990). Facts join on (currency, year); no per-row correlated lookup.
IF OBJECT_ID('dw.dim_fx') IS NULL
CREATE TABLE dw.dim_fx (
    currency_code CHAR(3)       NOT NULL,
    year          SMALLINT      NOT NULL,
    rate_to_usd   DECIMAL(14,6) NOT NULL,   -- local units per 1 USD → divide
    CONSTRAINT pk_dim_fx PRIMARY KEY (currency_code, year)
);
GO
