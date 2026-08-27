/* =============================================================
   dw.sales_wide — denormalised OBT (PostgreSQL port of 40_columnstore.sql)
   -------------------------------------------------------------
   SQL Server stores this one-big-table as a CLUSTERED COLUMNSTORE and
   adds 6 nonclustered columnstore indexes on the facts/rollups (HTAP).
   PostgreSQL has no columnstore, so:
     - sales_wide is a plain heap (no PK, matching the CCI-only source);
     - the ETL builds a BRIN on occurred_at (month-ordered load = a tiny,
       effective index for time-range scans on ~2.1B rows);
     - the 6 nonclustered columnstore indexes are dropped, replaced where
       useful by covering btrees built by the ETL.
   This divergence is deliberate curriculum: "same query, two engines,
   different physical design" (see NGE_POSTGRES_DW_PORT_PLAN.md).
   ============================================================= */
CREATE TABLE IF NOT EXISTS dw.sales_wide (
    transaction_line_id BIGINT        NOT NULL,
    transaction_id      BIGINT        NOT NULL,
    occurred_at         TIMESTAMP(3)  NOT NULL,
    date_key            INTEGER       NOT NULL,
    year                SMALLINT      NOT NULL,
    month               SMALLINT      NOT NULL,
    product_key         BIGINT        NOT NULL,
    product_title       VARCHAR(450)  NULL,
    product_kind        VARCHAR(12)   NULL,
    platform_name       VARCHAR(100)  NULL,
    genre               VARCHAR(120)  NULL,
    publisher           VARCHAR(300)  NULL,
    customer_key        BIGINT        NULL,
    customer_country    CHAR(2)       NULL,
    customer_region     VARCHAR(32)   NULL,
    loyalty_tier        VARCHAR(16)   NULL,
    shop_key            BIGINT        NULL,
    shop_name           VARCHAR(255)  NULL,
    shop_country        CHAR(2)       NULL,
    shop_region         VARCHAR(32)   NULL,
    channel_name        VARCHAR(24)   NULL,
    condition_name      VARCHAR(16)   NULL,
    currency_code       CHAR(3)       NOT NULL,
    quantity            INTEGER       NOT NULL,
    line_total          DECIMAL(12,2) NOT NULL,
    line_total_usd      DECIMAL(14,4) NOT NULL,
    is_return           BOOLEAN       NOT NULL,
    is_hardware         BOOLEAN       NOT NULL
);
