-- dw: fact tables. fact_sales grain = dbo.transaction_lines (a product line),
-- loaded month-by-month; clustered on occurred_at, PK nonclustered on the line key.
-- No fact→dim FKs (DW convention). FX-normalised to USD via dbo.fx_rates (usd = local / rate).

-- fact_sales
IF OBJECT_ID('dw.fact_sales') IS NULL
BEGIN
    CREATE TABLE dw.fact_sales (
        transaction_line_id BIGINT       NOT NULL,
        transaction_id      BIGINT       NOT NULL,
        occurred_at         DATETIME2(3) NOT NULL,
        date_key            INT          NOT NULL,
        product_key         BIGINT       NOT NULL,
        customer_key        BIGINT       NULL,
        shop_key            BIGINT       NULL,
        staff_key           BIGINT       NULL,
        channel_key         TINYINT      NULL,
        condition_key       TINYINT      NULL,
        currency_code       CHAR(3)      NOT NULL,
        quantity            INT          NOT NULL,
        unit_price          DECIMAL(12,2) NOT NULL,
        line_discount       DECIMAL(12,2) NOT NULL,
        line_tax            DECIMAL(12,2) NOT NULL,
        line_total          DECIMAL(12,2) NOT NULL,   -- transaction currency
        line_total_usd      DECIMAL(14,4) NOT NULL,   -- FX-normalised
        is_return           BIT          NOT NULL,     -- original_transaction_id present
        is_hardware         BIT          NOT NULL,
        CONSTRAINT pk_fact_sales PRIMARY KEY NONCLUSTERED (transaction_line_id)
    );
    CREATE CLUSTERED INDEX cx_fact_sales_occurred ON dw.fact_sales(occurred_at);
    CREATE NONCLUSTERED INDEX ix_fact_sales_product ON dw.fact_sales(product_key);
    CREATE NONCLUSTERED INDEX ix_fact_sales_customer ON dw.fact_sales(customer_key);
    CREATE NONCLUSTERED INDEX ix_fact_sales_shop ON dw.fact_sales(shop_key);
    CREATE NONCLUSTERED INDEX ix_fact_sales_date ON dw.fact_sales(date_key);
END
GO

-- fact_tradein: grain = dbo.trade_in_items
IF OBJECT_ID('dw.fact_tradein') IS NULL
BEGIN
    CREATE TABLE dw.fact_tradein (
        trade_in_item_id BIGINT       NOT NULL,
        trade_in_id      BIGINT       NOT NULL,
        occurred_at      DATETIME2(3) NOT NULL,
        date_key         INT          NOT NULL,
        product_key      BIGINT       NULL,
        customer_key     BIGINT       NULL,
        shop_key         BIGINT       NULL,
        condition_key    TINYINT      NULL,
        currency_code    CHAR(3)      NULL,
        offer_amount     DECIMAL(12,2) NULL,
        offer_amount_usd DECIMAL(14,4) NULL,
        is_hardware      BIT          NOT NULL,
        CONSTRAINT pk_fact_tradein PRIMARY KEY NONCLUSTERED (trade_in_item_id)
    );
    CREATE CLUSTERED INDEX cx_fact_tradein_occurred ON dw.fact_tradein(occurred_at);
    CREATE NONCLUSTERED INDEX ix_fact_tradein_product ON dw.fact_tradein(product_key);
END
GO

-- fact_web_activity: grain = web.page_views
IF OBJECT_ID('dw.fact_web_activity') IS NULL
BEGIN
    CREATE TABLE dw.fact_web_activity (
        page_view_id     BIGINT       NOT NULL,
        session_id       BIGINT       NOT NULL,
        occurred_at      DATETIME2(3) NOT NULL,
        date_key         INT          NOT NULL,
        account_id       BIGINT       NULL,
        customer_key     BIGINT       NULL,
        url_path         NVARCHAR(400) NULL,
        product_key      BIGINT       NULL,   -- parsed from /reviews/<release_id> paths
        client_country   CHAR(2)      NULL,
        user_agent_family NVARCHAR(40) NULL,
        is_bot           BIT          NOT NULL,
        http_status      INT          NULL,
        bytes_sent       INT          NULL,
        CONSTRAINT pk_fact_web PRIMARY KEY NONCLUSTERED (page_view_id)
    );
    CREATE CLUSTERED INDEX cx_fact_web_occurred ON dw.fact_web_activity(occurred_at);
END
GO
