-- dw: conformed dimensions. Natural business keys used where clean; a unified
-- surrogate is minted only for dim_product. SCD-1 (overwrite). Populated by batch.usp_refresh_dim_*.

-- dim_date: one row per calendar day
IF OBJECT_ID('dw.dim_date') IS NULL
CREATE TABLE dw.dim_date (
    date_key        INT          NOT NULL PRIMARY KEY,   -- yyyymmdd
    full_date       DATE         NOT NULL UNIQUE,
    year            SMALLINT     NOT NULL,
    quarter         TINYINT      NOT NULL,
    month           TINYINT      NOT NULL,
    month_name      NVARCHAR(12) NOT NULL,
    week_of_year     TINYINT     NOT NULL,
    day_of_month     TINYINT     NOT NULL,
    day_of_week      TINYINT     NOT NULL,               -- 1=Mon..7=Sun
    day_name        NVARCHAR(12) NOT NULL,
    is_weekend      BIT          NOT NULL,
    is_holiday_season BIT        NOT NULL,               -- Nov/Dec
    era             NVARCHAR(16) NOT NULL,               -- founding|growth|peak|decline|fire_sale|closed
    is_fire_sale    BIT          NOT NULL                -- >= 2016-04-02
);
GO

-- dim_geography: country → region → market
IF OBJECT_ID('dw.dim_geography') IS NULL
CREATE TABLE dw.dim_geography (
    country_code     CHAR(2)      NOT NULL PRIMARY KEY,
    country_name     NVARCHAR(64) NOT NULL,
    region           NVARCHAR(32) NOT NULL,             -- North America|Europe|...
    market           NVARCHAR(32) NOT NULL,             -- coarse: NA|EU|JP|LATAM|APAC
    default_currency CHAR(3)      NOT NULL,
    governing_regime NVARCHAR(16) NULL
);
GO

-- dim_currency
IF OBJECT_ID('dw.dim_currency') IS NULL
CREATE TABLE dw.dim_currency (
    currency_code CHAR(3)      NOT NULL PRIMARY KEY,
    name          NVARCHAR(64) NOT NULL,
    minor_unit    TINYINT      NOT NULL
);
GO

-- dim_platform
IF OBJECT_ID('dw.dim_platform') IS NULL
CREATE TABLE dw.dim_platform (
    platform_key      INT          NOT NULL PRIMARY KEY,  -- = dbo.platforms.platform_id
    platform_name     NVARCHAR(100) NOT NULL,
    family            NVARCHAR(50) NULL,                  -- Sony|Nintendo|Sega|...
    released_year     SMALLINT     NULL,
    discontinued_year SMALLINT     NULL
);
GO

-- dim_product: games XOR hardware, unified.
-- product_key = release_id for games; 9,000,000,000 + hardware_id for hardware (ranges never collide).
IF OBJECT_ID('dw.dim_product') IS NULL
CREATE TABLE dw.dim_product (
    product_key    BIGINT        NOT NULL PRIMARY KEY,
    product_kind   NVARCHAR(12)  NOT NULL,               -- 'game' | 'hardware'
    release_id     BIGINT        NULL,                   -- source key (game)
    hardware_id    INT           NULL,                   -- source key (hardware)
    title          NVARCHAR(450) NOT NULL,               -- game title or model_name
    platform_key   INT           NULL,
    platform_name  NVARCHAR(100) NULL,
    category        NVARCHAR(120) NULL,                  -- genre (game) / kind (hardware)
    publisher      NVARCHAR(300) NULL,
    developer      NVARCHAR(300) NULL,
    manufacturer   NVARCHAR(80)  NULL,
    media_type     NVARCHAR(50)  NULL,
    first_release_date DATE      NULL,
    launch_usd     DECIMAL(10,2) NULL,
    CONSTRAINT ck_dim_product_kind CHECK (product_kind IN ('game','hardware'))
);
GO

-- dim_customer
IF OBJECT_ID('dw.dim_customer') IS NULL
CREATE TABLE dw.dim_customer (
    customer_key   BIGINT        NOT NULL PRIMARY KEY,   -- = dbo.customers.customer_id
    status         NVARCHAR(16)  NOT NULL,
    signup_date    DATE          NOT NULL,
    signup_year    SMALLINT      NOT NULL,
    signup_cohort  CHAR(7)       NOT NULL,               -- 'YYYY-MM'
    country_code   CHAR(2)       NULL,
    region         NVARCHAR(32)  NULL,
    city           NVARCHAR(200) NULL,
    loyalty_tier   NVARCHAR(16)  NULL,                   -- bronze|silver|gold|platinum
    is_anonymised  BIT           NOT NULL,
    birth_year     SMALLINT      NULL,
    age_band       NVARCHAR(12)  NULL                    -- <18|18-24|25-34|35-44|45-54|55+
);
GO

-- dim_shop
IF OBJECT_ID('dw.dim_shop') IS NULL
CREATE TABLE dw.dim_shop (
    shop_key      BIGINT        NOT NULL PRIMARY KEY,    -- = dbo.shops.shop_id
    shop_code     NVARCHAR(16)  NOT NULL,
    name          NVARCHAR(255) NOT NULL,
    country_code  CHAR(2)       NOT NULL,
    region        NVARCHAR(32)  NULL,
    city          NVARCHAR(200) NULL,
    opened_date   DATE          NOT NULL,
    closed_date   DATE          NULL,
    is_open       BIT           NOT NULL,                -- closed_date IS NULL
    currency_code CHAR(3)       NOT NULL,
    is_flagship   BIT           NOT NULL                 -- shop_id IN (1,2)
);
GO

-- dim_staff: one row per employment spell
IF OBJECT_ID('dw.dim_staff') IS NULL
CREATE TABLE dw.dim_staff (
    staff_key     BIGINT        NOT NULL PRIMARY KEY,    -- = hr.employment_spells.employment_spell_id
    person_id     BIGINT        NULL,
    full_name     NVARCHAR(200) NULL,
    role_title    NVARCHAR(120) NULL,
    home_shop_id  BIGINT        NULL,
    started_on    DATE          NULL,
    ended_on      DATE          NULL
);
GO

-- small static dimensions
IF OBJECT_ID('dw.dim_channel') IS NULL
CREATE TABLE dw.dim_channel (
    channel_key  TINYINT      NOT NULL PRIMARY KEY,
    channel_name NVARCHAR(24) NOT NULL UNIQUE
);
GO
IF OBJECT_ID('dw.dim_condition') IS NULL
CREATE TABLE dw.dim_condition (
    condition_key  TINYINT      NOT NULL PRIMARY KEY,
    condition_name NVARCHAR(16) NOT NULL UNIQUE,
    is_used        BIT          NOT NULL
);
GO
