-- dw: columnstore layer, additive to the rowstore:
--   1. dw.sales_wide — denormalised OBT as a CLUSTERED COLUMNSTORE (fact_sales + dimension attributes).
--   2. NONCLUSTERED columnstore indexes on the rowstore facts + big rollups (via batch.usp_build_columnstore).

-- sales_wide: clustered-columnstore OBT
IF OBJECT_ID('dw.sales_wide') IS NULL
BEGIN
    CREATE TABLE dw.sales_wide (
        transaction_line_id BIGINT       NOT NULL,
        transaction_id      BIGINT       NOT NULL,
        occurred_at         DATETIME2(3) NOT NULL,
        date_key            INT          NOT NULL,
        year                SMALLINT     NOT NULL,
        month               TINYINT      NOT NULL,
        product_key         BIGINT       NOT NULL,
        product_title       NVARCHAR(450) NULL,
        product_kind        NVARCHAR(12) NULL,
        platform_name       NVARCHAR(100) NULL,
        genre               NVARCHAR(120) NULL,
        publisher           NVARCHAR(300) NULL,
        customer_key        BIGINT       NULL,
        customer_country    CHAR(2)      NULL,
        customer_region     NVARCHAR(32) NULL,
        loyalty_tier        NVARCHAR(16) NULL,
        shop_key            BIGINT       NULL,
        shop_name           NVARCHAR(255) NULL,
        shop_country        CHAR(2)      NULL,
        shop_region         NVARCHAR(32) NULL,
        channel_name        NVARCHAR(24) NULL,
        condition_name      NVARCHAR(16) NULL,
        currency_code       CHAR(3)      NOT NULL,
        quantity            INT          NOT NULL,
        line_total          DECIMAL(12,2) NOT NULL,
        line_total_usd      DECIMAL(14,4) NOT NULL,
        is_return           BIT          NOT NULL,
        is_hardware         BIT          NOT NULL
    );
    CREATE CLUSTERED COLUMNSTORE INDEX cci_sales_wide ON dw.sales_wide;
END
GO
