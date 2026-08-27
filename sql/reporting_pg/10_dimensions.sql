/* =============================================================
   dw — conformed dimensions (PostgreSQL port of 10_dimensions.sql)
   -------------------------------------------------------------
   Natural business keys where clean; a unified surrogate only for
   dim_product (games XOR hardware). SCD-1 (overwrite). Populated by
   the batch.usp_refresh_dim_* procedures — these are empty shells.
   ============================================================= */

/* ---- dim_date : one row per calendar day, 1986-01-01 -> present ---- */
CREATE TABLE IF NOT EXISTS dw.dim_date (
    date_key          INTEGER      NOT NULL PRIMARY KEY,   -- yyyymmdd
    full_date         DATE         NOT NULL UNIQUE,
    year              SMALLINT     NOT NULL,
    quarter           SMALLINT     NOT NULL,
    month             SMALLINT     NOT NULL,
    month_name        VARCHAR(12)  NOT NULL,
    week_of_year      SMALLINT     NOT NULL,
    day_of_month      SMALLINT     NOT NULL,
    day_of_week       SMALLINT     NOT NULL,               -- 1=Mon..7=Sun
    day_name          VARCHAR(12)  NOT NULL,
    is_weekend        BOOLEAN      NOT NULL,
    is_holiday_season BOOLEAN      NOT NULL,               -- Nov/Dec
    era               VARCHAR(16)  NOT NULL,               -- founding|growth|peak|decline|fire_sale|closed
    is_fire_sale      BOOLEAN      NOT NULL                -- >= 2016-04-02
);

/* ---- dim_geography : country -> region -> market ---- */
CREATE TABLE IF NOT EXISTS dw.dim_geography (
    country_code     CHAR(2)      NOT NULL PRIMARY KEY,
    country_name     VARCHAR(64)  NOT NULL,
    region           VARCHAR(32)  NOT NULL,               -- North America|Europe|...
    market           VARCHAR(32)  NOT NULL,               -- NA|EU|JP|LATAM|APAC
    default_currency CHAR(3)      NOT NULL,
    governing_regime VARCHAR(16)  NULL
);

/* ---- dim_currency ---- */
CREATE TABLE IF NOT EXISTS dw.dim_currency (
    currency_code CHAR(3)      NOT NULL PRIMARY KEY,
    name          VARCHAR(64)  NOT NULL,
    minor_unit    SMALLINT     NOT NULL
);

/* ---- dim_platform ---- */
CREATE TABLE IF NOT EXISTS dw.dim_platform (
    platform_key      INTEGER      NOT NULL PRIMARY KEY,   -- = dbo.platforms.platform_id
    platform_name     VARCHAR(100) NOT NULL,
    family            VARCHAR(50)  NULL,                   -- Sony|Nintendo|Sega|...
    released_year     SMALLINT     NULL,
    discontinued_year SMALLINT     NULL
);

/* ---- dim_product : games XOR hardware, unified.
   product_key = release_id for games; 9,000,000,000 + hardware_id for
   hardware (releases << 9e9, so ranges never collide). ---- */
CREATE TABLE IF NOT EXISTS dw.dim_product (
    product_key        BIGINT       NOT NULL PRIMARY KEY,
    product_kind       VARCHAR(12)  NOT NULL,             -- 'game' | 'hardware'
    release_id         BIGINT       NULL,                 -- source key (game)
    hardware_id        INTEGER      NULL,                 -- source key (hardware)
    title              VARCHAR(450) NOT NULL,             -- game title or model_name
    platform_key       INTEGER      NULL,
    platform_name      VARCHAR(100) NULL,
    category           VARCHAR(120) NULL,                 -- genre (game) / kind (hardware)
    publisher          VARCHAR(300) NULL,
    developer          VARCHAR(300) NULL,
    manufacturer       VARCHAR(80)  NULL,
    media_type         VARCHAR(50)  NULL,
    first_release_date DATE         NULL,
    launch_usd         DECIMAL(10,2) NULL,
    CONSTRAINT ck_dim_product_kind CHECK (product_kind IN ('game','hardware'))
);

/* ---- dim_customer (~39.4M) ---- */
CREATE TABLE IF NOT EXISTS dw.dim_customer (
    customer_key  BIGINT       NOT NULL PRIMARY KEY,      -- = dbo.customers.customer_id
    status        VARCHAR(16)  NOT NULL,
    signup_date   DATE         NOT NULL,
    signup_year   SMALLINT     NOT NULL,
    signup_cohort CHAR(7)      NOT NULL,                  -- 'YYYY-MM'
    country_code  CHAR(2)      NULL,
    region        VARCHAR(32)  NULL,
    city          VARCHAR(200) NULL,
    loyalty_tier  VARCHAR(16)  NULL,                      -- bronze|silver|gold|platinum
    is_anonymised BOOLEAN      NOT NULL,
    birth_year    SMALLINT     NULL,
    age_band      VARCHAR(12)  NULL                       -- <18|18-24|25-34|35-44|45-54|55+
);

/* ---- dim_shop (~8,260) ---- */
CREATE TABLE IF NOT EXISTS dw.dim_shop (
    shop_key      BIGINT       NOT NULL PRIMARY KEY,      -- = dbo.shops.shop_id
    shop_code     VARCHAR(16)  NOT NULL,
    name          VARCHAR(255) NOT NULL,
    country_code  CHAR(2)      NOT NULL,
    region        VARCHAR(32)  NULL,
    city          VARCHAR(200) NULL,
    opened_date   DATE         NOT NULL,
    closed_date   DATE         NULL,
    is_open       BOOLEAN      NOT NULL,                  -- closed_date IS NULL
    currency_code CHAR(3)      NOT NULL,
    is_flagship   BOOLEAN      NOT NULL                   -- shop_id IN (1,2) — founding stores
);

/* ---- dim_staff : one row per employment spell (transactions.staff_id) ---- */
CREATE TABLE IF NOT EXISTS dw.dim_staff (
    staff_key    BIGINT       NOT NULL PRIMARY KEY,       -- = hr.employment_spells.employment_spell_id
    person_id    BIGINT       NULL,
    full_name    VARCHAR(200) NULL,
    role_title   VARCHAR(120) NULL,
    home_shop_id BIGINT       NULL,
    started_on   DATE         NULL,
    ended_on     DATE         NULL
);

/* ---- small static dimensions ---- */
CREATE TABLE IF NOT EXISTS dw.dim_channel (
    channel_key  SMALLINT     NOT NULL PRIMARY KEY,
    channel_name VARCHAR(24)  NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS dw.dim_condition (
    condition_key  SMALLINT     NOT NULL PRIMARY KEY,
    condition_name VARCHAR(16)  NOT NULL UNIQUE,
    is_used        BOOLEAN      NOT NULL
);
