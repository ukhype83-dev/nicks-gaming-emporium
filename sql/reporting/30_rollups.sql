-- dw: aggregate rollups — batch-refreshed materialised summary tables over the facts (+ web).

IF OBJECT_ID('dw.agg_sales_by_month') IS NULL
CREATE TABLE dw.agg_sales_by_month (
    year_month        CHAR(7)       NOT NULL PRIMARY KEY,
    line_count        BIGINT        NOT NULL,
    tx_count          BIGINT        NOT NULL,
    units             BIGINT        NOT NULL,
    gross_revenue_usd DECIMAL(18,2) NOT NULL,   -- forward lines only
    return_count      BIGINT        NOT NULL,
    return_value_usd  DECIMAL(18,2) NOT NULL,
    net_revenue_usd   DECIMAL(18,2) NOT NULL,   -- incl. returns (negative)
    software_revenue_usd DECIMAL(18,2) NOT NULL,
    hardware_revenue_usd DECIMAL(18,2) NOT NULL,
    avg_line_usd      DECIMAL(12,4) NOT NULL
);
GO

IF OBJECT_ID('dw.agg_sales_by_month_platform') IS NULL
CREATE TABLE dw.agg_sales_by_month_platform (
    year_month    CHAR(7)       NOT NULL,
    platform_key  INT           NOT NULL,
    platform_name NVARCHAR(100) NULL,
    units         BIGINT        NOT NULL,
    line_count    BIGINT        NOT NULL,
    revenue_usd   DECIMAL(18,2) NOT NULL,
    CONSTRAINT pk_agg_sbmp PRIMARY KEY (year_month, platform_key)
);
GO

IF OBJECT_ID('dw.agg_sales_by_day_shop') IS NULL
CREATE TABLE dw.agg_sales_by_day_shop (
    date_key    INT           NOT NULL,
    shop_key    BIGINT        NOT NULL,
    tx_count    BIGINT        NOT NULL,
    line_count  BIGINT        NOT NULL,
    units       BIGINT        NOT NULL,
    revenue_usd DECIMAL(18,2) NOT NULL,
    CONSTRAINT pk_agg_sbds PRIMARY KEY (date_key, shop_key)
);
GO

IF OBJECT_ID('dw.agg_product_performance') IS NULL
CREATE TABLE dw.agg_product_performance (
    product_key       BIGINT        NOT NULL PRIMARY KEY,
    first_sold        DATE          NULL,
    last_sold         DATE          NULL,
    units_sold        BIGINT        NOT NULL,
    line_count        BIGINT        NOT NULL,
    revenue_usd       DECIMAL(18,2) NOT NULL,
    return_count      BIGINT        NOT NULL,
    distinct_customers BIGINT       NOT NULL
);
GO

IF OBJECT_ID('dw.agg_customer_ltv') IS NULL
CREATE TABLE dw.agg_customer_ltv (
    customer_key    BIGINT        NOT NULL PRIMARY KEY,
    first_purchase  DATE          NULL,
    last_purchase   DATE          NULL,
    order_count     BIGINT        NOT NULL,   -- distinct transactions
    line_count      BIGINT        NOT NULL,
    units           BIGINT        NOT NULL,
    gross_spend_usd DECIMAL(18,2) NOT NULL,
    returns_usd     DECIMAL(18,2) NOT NULL,
    net_spend_usd   DECIMAL(18,2) NOT NULL
);
GO

IF OBJECT_ID('dw.agg_web_traffic_daily') IS NULL
CREATE TABLE dw.agg_web_traffic_daily (
    date_key         INT    NOT NULL PRIMARY KEY,
    page_views       BIGINT NOT NULL,
    sessions         BIGINT NOT NULL,
    bot_views        BIGINT NOT NULL,
    human_views      BIGINT NOT NULL,
    logged_in_views  BIGINT NOT NULL,
    distinct_countries INT  NOT NULL
);
GO

IF OBJECT_ID('dw.agg_review_sentiment_by_month') IS NULL
CREATE TABLE dw.agg_review_sentiment_by_month (
    year_month        CHAR(7)      NOT NULL PRIMARY KEY,
    review_count      BIGINT       NOT NULL,
    avg_rating        DECIMAL(4,3) NOT NULL,
    rating_only_count BIGINT       NOT NULL,
    verified_count    BIGINT       NOT NULL
);
GO
