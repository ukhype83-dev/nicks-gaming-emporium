/* =============================================================
   dw — fact tables (PostgreSQL port of 20_facts.sql)
   -------------------------------------------------------------
   fact_sales grain = dbo.transaction_lines. Loaded month-by-month by
   batch.usp_refresh_fact_sales (a PROCEDURE that COMMITs per month so
   the load is never one giant transaction). No fact->dim FKs (DW
   convention). PK on the natural line key for reconciliation.

   SQL Server clusters each fact on occurred_at and adds nonclustered
   secondary indexes; Postgres has no clustered-index storage, so the
   ETL builds a BRIN on occurred_at (ideal for the month-ordered load)
   plus covering btrees AFTER the bulk load — the faithful analogue of
   SQL Server's DISABLE-load-REBUILD. Only the tables + PKs live here.
   ============================================================= */

/* ---- fact_sales ---- */
CREATE TABLE IF NOT EXISTS dw.fact_sales (
    transaction_line_id BIGINT        NOT NULL,
    transaction_id      BIGINT        NOT NULL,
    occurred_at         TIMESTAMP(3)  NOT NULL,
    date_key            INTEGER       NOT NULL,
    product_key         BIGINT        NOT NULL,
    customer_key        BIGINT        NULL,
    shop_key            BIGINT        NULL,
    staff_key           BIGINT        NULL,
    channel_key         SMALLINT      NULL,
    condition_key       SMALLINT      NULL,
    currency_code       CHAR(3)       NOT NULL,
    quantity            INTEGER       NOT NULL,
    unit_price          DECIMAL(12,2) NOT NULL,
    line_discount       DECIMAL(12,2) NOT NULL,
    line_tax            DECIMAL(12,2) NOT NULL,
    line_total          DECIMAL(12,2) NOT NULL,   -- transaction currency
    line_total_usd      DECIMAL(14,4) NOT NULL,   -- FX-normalised
    is_return           BOOLEAN       NOT NULL,    -- original_transaction_id present
    is_hardware         BOOLEAN       NOT NULL,
    CONSTRAINT pk_fact_sales PRIMARY KEY (transaction_line_id)
);

/* ---- fact_tradein : grain = dbo.trade_in_items ---- */
CREATE TABLE IF NOT EXISTS dw.fact_tradein (
    trade_in_item_id BIGINT        NOT NULL,
    trade_in_id      BIGINT        NOT NULL,
    occurred_at      TIMESTAMP(3)  NOT NULL,
    date_key         INTEGER       NOT NULL,
    product_key      BIGINT        NULL,
    customer_key     BIGINT        NULL,
    shop_key         BIGINT        NULL,
    condition_key    SMALLINT      NULL,
    currency_code    CHAR(3)       NULL,
    offer_amount     DECIMAL(12,2) NULL,
    offer_amount_usd DECIMAL(14,4) NULL,
    is_hardware      BOOLEAN       NOT NULL,
    CONSTRAINT pk_fact_tradein PRIMARY KEY (trade_in_item_id)
);

/* ---- fact_web_activity : grain = web.page_views ---- */
CREATE TABLE IF NOT EXISTS dw.fact_web_activity (
    page_view_id      BIGINT        NOT NULL,
    session_id        BIGINT        NOT NULL,
    occurred_at       TIMESTAMP(3)  NOT NULL,
    date_key          INTEGER       NOT NULL,
    account_id        BIGINT        NULL,
    customer_key      BIGINT        NULL,
    url_path          VARCHAR(400)  NULL,
    product_key       BIGINT        NULL,   -- parsed from /reviews/<release_id> paths
    client_country    CHAR(2)       NULL,
    user_agent_family VARCHAR(40)   NULL,
    is_bot            BOOLEAN       NOT NULL,
    http_status       INTEGER       NULL,
    bytes_sent        INTEGER       NULL,
    CONSTRAINT pk_fact_web PRIMARY KEY (page_view_id)
);
