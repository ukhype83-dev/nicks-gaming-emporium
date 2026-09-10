-- Nick's Gaming Emporium — V1 schema (SQL Server 2016+)

-- Schemas: retail tables live in dbo; hr and finance keep their own schemas.
IF SCHEMA_ID('hr')      IS NULL EXEC('CREATE SCHEMA hr');
GO

-- retail — reference / lookup tables

-- source-system enum; joined to every transactional row to drive era-specific behaviour
CREATE TABLE dbo.source_systems (
    source_system   NVARCHAR(48)  NOT NULL PRIMARY KEY,
    nature          NVARCHAR(16)  NOT NULL,  -- 'migrated' | 'native'
    effective_from  DATE          NOT NULL,
    effective_to    DATE          NULL,
    description     NVARCHAR(200) NOT NULL,
    CONSTRAINT ck_source_systems_nature CHECK (nature IN ('migrated','native'))
);

CREATE TABLE dbo.privacy_regimes (
    regime          NVARCHAR(16)  NOT NULL PRIMARY KEY,
    effective_from  DATE          NOT NULL,
    retention_days  INT           NULL,        -- max retention from last activity
    deletion_sla_days INT         NULL,        -- regulatory response SLA
    description     NVARCHAR(200) NOT NULL
);

CREATE TABLE dbo.payment_methods (
    method          NVARCHAR(32)  NOT NULL PRIMARY KEY,
    introduced      DATE          NOT NULL,
    retired         DATE          NULL,
    channel_scope   NVARCHAR(32)  NULL,        -- 'online_only' | NULL (all)
    description     NVARCHAR(200) NOT NULL
);

CREATE TABLE dbo.currencies (
    currency_code   CHAR(3)       NOT NULL PRIMARY KEY,  -- ISO 4217
    name            NVARCHAR(64)  NOT NULL,
    minor_unit      TINYINT       NOT NULL      -- e.g. JPY=0, USD=2
);

CREATE TABLE dbo.countries (
    country_code    CHAR(2)       NOT NULL PRIMARY KEY,  -- ISO 3166-1 alpha-2
    name            NVARCHAR(64)  NOT NULL,
    default_currency CHAR(3)      NOT NULL REFERENCES dbo.currencies(currency_code),
    governing_regime NVARCHAR(16) NULL REFERENCES dbo.privacy_regimes(regime)
);

-- Annual FX rates against USD. rate_to_usd = units of foreign currency per 1 USD;
-- multiply a JPY total by 1/rate_to_usd for USD-equivalent value. Currencies are
-- absent for eras before they existed (EUR pre-1999, BRL pre-1994, PLN pre-1995, CZK pre-1993).
CREATE TABLE dbo.fx_rates (
    currency_code   CHAR(3)       NOT NULL REFERENCES dbo.currencies(currency_code),
    effective_year  SMALLINT      NOT NULL,
    rate_to_usd     DECIMAL(14,6) NOT NULL,
    CONSTRAINT pk_fx_rates PRIMARY KEY (currency_code, effective_year),
    CONSTRAINT ck_fx_rates_positive CHECK (rate_to_usd > 0)
);

-- normalises the platform string on releases
CREATE TABLE dbo.platforms (
    platform_id     INT           NOT NULL PRIMARY KEY,
    name            NVARCHAR(100) NOT NULL UNIQUE,
    family          NVARCHAR(50)  NULL,       -- 'Sony' | 'Nintendo' | 'Sega' | etc.
    released_year   SMALLINT      NULL,
    discontinued_year SMALLINT    NULL
);
GO

-- retail — catalog

-- one row per released SKU (Sonic on Genesis / Master System / Game Gear = three rows)
CREATE TABLE dbo.releases (
    release_id          BIGINT         NOT NULL PRIMARY KEY,
    title               NVARCHAR(MAX)  NOT NULL,  -- PS2 multi-region denorm rows hit ~960 chars
    normalised_title    NVARCHAR(450)  NULL,      -- 450 is the NVARCHAR nonclustered-index key-length limit
    platform_id         INT            NOT NULL REFERENCES dbo.platforms(platform_id),
    media_type          NVARCHAR(50)   NULL,
    publisher           NVARCHAR(300)  NULL,     -- raw
    developer           NVARCHAR(300)  NULL,
    genre               NVARCHAR(120)  NULL,
    first_release_date  DATE           NULL,
    first_release_raw   NVARCHAR(200)  NULL,     -- multi-region scraped strings: "200407 20040729 July 29, 2004 (JP) ..."
    release_jp          DATE           NULL,
    release_na          DATE           NULL,
    release_eu          DATE           NULL,
    release_br          DATE           NULL,
    released_jp         BIT            NULL,
    released_na         BIT            NULL,
    released_eu         BIT            NULL,
    released_br         BIT            NULL
);
GO

-- console / hardware catalog: one row per hardware model (base + revisions);
-- revision_of self-refs the base model (slim -> fat)
CREATE TABLE dbo.hardware (
    hardware_id    INT           NOT NULL PRIMARY KEY,
    model_name     NVARCHAR(120) NOT NULL,
    platform_id    INT           NOT NULL REFERENCES dbo.platforms(platform_id),
    kind           NVARCHAR(16)  NOT NULL,   -- 'console'|'handheld'|'computer'|'accessory'
    manufacturer   NVARCHAR(80)  NULL,
    model_number   NVARCHAR(60)  NULL,
    release_na     DATE          NULL,
    release_jp     DATE          NULL,
    release_eu     DATE          NULL,
    launch_usd     DECIMAL(10,2) NULL,
    revision_of    INT           NULL REFERENCES dbo.hardware(hardware_id),
    notes          NVARCHAR(400) NULL,
    CONSTRAINT ck_hardware_kind CHECK (kind IN ('console','handheld','computer','accessory'))
);
GO

-- retail — operations (shops, inventory)

CREATE TABLE dbo.shops (
    shop_id         BIGINT        NOT NULL PRIMARY KEY,
    shop_code       NVARCHAR(16)  NOT NULL UNIQUE,    -- 'GB-LON-001'
    name            NVARCHAR(255) NOT NULL,
    country_code    CHAR(2)       NOT NULL REFERENCES dbo.countries(country_code),
    opened_date     DATE          NOT NULL,
    closed_date     DATE          NULL,
    currency_code   CHAR(3)       NOT NULL REFERENCES dbo.currencies(currency_code),
    source_system   NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system)
);

-- per-shop addresses; not effective-dated (shops rarely move)
CREATE TABLE dbo.shop_addresses (
    shop_address_id BIGINT        NOT NULL PRIMARY KEY,
    shop_id         BIGINT        NOT NULL REFERENCES dbo.shops(shop_id),
    line1           NVARCHAR(200) NOT NULL,
    line2           NVARCHAR(200) NULL,
    city            NVARCHAR(500) NOT NULL,  -- GeoNames CA FSA entries have verbose district descriptors
    region          NVARCHAR(500) NULL,
    postal_code     NVARCHAR(20)  NULL,
    country_code    CHAR(2)       NOT NULL REFERENCES dbo.countries(country_code),
    latitude        DECIMAL(9,6)  NULL,
    longitude       DECIMAL(9,6)  NULL
);

CREATE TABLE dbo.inventory (
    inventory_id    BIGINT        NOT NULL PRIMARY KEY,
    shop_id         BIGINT        NOT NULL REFERENCES dbo.shops(shop_id),
    -- a stock row is a software SKU (release_id) XOR a hardware model (hardware_id)
    release_id      BIGINT        NULL REFERENCES dbo.releases(release_id),
    hardware_id     INT           NULL REFERENCES dbo.hardware(hardware_id),
    condition       NVARCHAR(16)  NOT NULL,  -- 'new' | 'used_mint' | 'used_good' | 'used_fair' | 'used_loose'
    on_hand         INT           NOT NULL DEFAULT 0,
    on_order        INT           NOT NULL DEFAULT 0,
    reserved        INT           NOT NULL DEFAULT 0,
    last_movement_at DATETIME2(3) NULL,
    CONSTRAINT ck_inventory_condition CHECK (condition IN (
        'new','used_mint','used_good','used_fair','used_loose'
    )),
    CONSTRAINT ck_inventory_sku_xor CHECK (
        (release_id IS NOT NULL AND hardware_id IS NULL) OR
        (release_id IS NULL AND hardware_id IS NOT NULL)
    ),
    CONSTRAINT ck_inventory_nonneg CHECK (on_hand >= 0 AND on_order >= 0 AND reserved >= 0)
);

CREATE TABLE dbo.inventory_movements (
    movement_id      BIGINT        NOT NULL PRIMARY KEY,
    inventory_id     BIGINT        NOT NULL REFERENCES dbo.inventory(inventory_id),
    occurred_at      DATETIME2(3)  NOT NULL,
    occurred_at_precision NVARCHAR(12) NOT NULL,  -- 'year'|'month'|'day'|'hour'|'minute'|'second'|'millisecond'
    movement_type    NVARCHAR(24)  NOT NULL,      -- 'sale'|'trade_in'|'delivery'|'transfer_in'|'transfer_out'|'adjustment'|'return'
    quantity         INT           NOT NULL,      -- signed; sale is negative, delivery is positive
    reference_type   NVARCHAR(32)  NULL,          -- e.g. 'transaction' | 'trade_in'
    reference_id     BIGINT        NULL,          -- FK-less soft pointer (reference_type tells you which table)
    source_system    NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system),
    CONSTRAINT ck_inventory_movements_precision CHECK (occurred_at_precision IN (
        'year','month','day','hour','minute','second','millisecond'
    ))
);
GO

-- retail — customers & privacy

CREATE TABLE dbo.customers (
    customer_id         BIGINT        NOT NULL PRIMARY KEY,
    status              NVARCHAR(16)  NOT NULL DEFAULT 'active',  -- 'active'|'dormant'|'anonymised'|'deleted'
    signed_up_at        DATETIME2(3)  NOT NULL,
    governing_regime    NVARCHAR(16)  NOT NULL REFERENCES dbo.privacy_regimes(regime),
    first_name          NVARCHAR(100) NULL,       -- NULLable: anonymised / never-captured
    last_name           NVARCHAR(100) NULL,
    date_of_birth       DATE          NULL,
    source_system       NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system),
    anonymised_at       DATETIME2(3)  NULL,
    data_retention_expires_at DATETIME2(3) NULL,
    CONSTRAINT ck_customers_status CHECK (status IN ('active','dormant','anonymised','deleted'))
);

CREATE TABLE dbo.customer_addresses (
    customer_address_id BIGINT        NOT NULL PRIMARY KEY,
    customer_id         BIGINT        NOT NULL REFERENCES dbo.customers(customer_id),
    address_type        NVARCHAR(16)  NOT NULL,   -- 'billing'|'shipping'|'work'|'other'
    effective_from      DATE          NOT NULL,
    effective_to        DATE          NULL,       -- NULL = currently active
    line1               NVARCHAR(200) NULL,       -- NULLable when anonymised
    line2               NVARCHAR(200) NULL,
    city                NVARCHAR(500) NULL,       -- GeoNames CA FSA entries can be verbose
    region              NVARCHAR(500) NULL,
    postal_code         NVARCHAR(20)  NULL,
    country_code        CHAR(2)       NOT NULL REFERENCES dbo.countries(country_code),
    address_hash        NVARCHAR(64)  NULL,       -- SHA-256 of normalised form, survives anonymisation
    anonymised_at       DATETIME2(3)  NULL,
    source_system       NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system),
    CONSTRAINT ck_cust_addr_type CHECK (address_type IN ('billing','shipping','work','other'))
);

CREATE TABLE dbo.customer_emails (
    customer_email_id BIGINT       NOT NULL PRIMARY KEY,
    customer_id       BIGINT       NOT NULL REFERENCES dbo.customers(customer_id),
    email             NVARCHAR(320) NULL,       -- NULLable when anonymised
    is_primary        BIT          NOT NULL DEFAULT 0,
    verified_at       DATETIME2(3) NULL,
    anonymised_at     DATETIME2(3) NULL
);

CREATE TABLE dbo.communication_preferences (
    comm_pref_id      BIGINT        NOT NULL PRIMARY KEY,
    customer_id       BIGINT        NOT NULL REFERENCES dbo.customers(customer_id),
    channel           NVARCHAR(16)  NOT NULL,  -- 'email'|'sms'|'push'|'post'
    purpose           NVARCHAR(32)  NOT NULL,  -- 'marketing'|'service'|'trade_in_alerts'|...
    opt_in            BIT           NOT NULL,
    updated_at        DATETIME2(3)  NOT NULL,
    CONSTRAINT ck_comm_pref_channel CHECK (channel IN ('email','sms','push','post'))
);

-- timestamped consent events; point-in-time queries join on this
CREATE TABLE dbo.consent_events (
    consent_event_id BIGINT        NOT NULL PRIMARY KEY,
    customer_id      BIGINT        NOT NULL REFERENCES dbo.customers(customer_id),
    channel          NVARCHAR(16)  NOT NULL,
    purpose          NVARCHAR(32)  NOT NULL,
    event_type       NVARCHAR(16)  NOT NULL,   -- 'granted'|'revoked'
    occurred_at      DATETIME2(3)  NOT NULL,
    source_system    NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system),
    CONSTRAINT ck_consent_events_type CHECK (event_type IN ('granted','revoked'))
);

CREATE TABLE dbo.customer_lifecycle_events (
    lifecycle_event_id BIGINT       NOT NULL PRIMARY KEY,
    customer_id        BIGINT       NOT NULL REFERENCES dbo.customers(customer_id),
    event_type         NVARCHAR(32) NOT NULL,  -- 'signup'|'reactivated'|'marked_dormant'|'anonymisation_requested'|'anonymised'|'deleted'
    occurred_at        DATETIME2(3) NOT NULL,
    reason             NVARCHAR(64) NULL,      -- 'gdpr_erasure'|'ccpa_request'|'retention_expiry'|'fraud'
    source_system      NVARCHAR(48) NOT NULL REFERENCES dbo.source_systems(source_system)
);

CREATE TABLE dbo.loyalty_memberships (
    membership_id     BIGINT        NOT NULL PRIMARY KEY,
    customer_id       BIGINT        NOT NULL REFERENCES dbo.customers(customer_id),
    scheme            NVARCHAR(32)  NOT NULL,  -- 'stamp_1986'|'card_1998'|'unified_2011'
    enrolled_at       DATETIME2(3)  NOT NULL,
    tier              NVARCHAR(16)  NULL,       -- 'bronze'|'silver'|'gold'|'platinum'
    points_balance    INT           NOT NULL DEFAULT 0,
    closed_at         DATETIME2(3)  NULL
);

CREATE TABLE dbo.saved_payment_methods (
    saved_payment_id  BIGINT        NOT NULL PRIMARY KEY,
    customer_id       BIGINT        NOT NULL REFERENCES dbo.customers(customer_id),
    method            NVARCHAR(32)  NOT NULL REFERENCES dbo.payment_methods(method),
    token             NVARCHAR(128) NOT NULL,   -- processor-side token; no PAN stored
    card_last4        CHAR(4)       NULL,
    expiry_month      TINYINT       NULL,
    expiry_year       SMALLINT      NULL,
    added_at          DATETIME2(3)  NOT NULL,
    removed_at        DATETIME2(3)  NULL
);
GO

-- retail — transactions

CREATE TABLE dbo.transactions (
    transaction_id       BIGINT        NOT NULL PRIMARY KEY,
    occurred_at          DATETIME2(3)  NOT NULL,
    occurred_at_precision NVARCHAR(12) NOT NULL,
    shop_id              BIGINT        NULL REFERENCES dbo.shops(shop_id),
    channel              NVARCHAR(24)  NOT NULL,   -- 'in_store'|'phone'|'online'|'mobile_app'|'click_and_collect'
    customer_id          BIGINT        NULL REFERENCES dbo.customers(customer_id),
    shipping_address_id  BIGINT        NULL REFERENCES dbo.customer_addresses(customer_address_id),
    staff_id             BIGINT        NULL,       -- FK to hr.employment_spells declared later (cross-schema ordering)
    -- till_id is a string: values like '1'-'4' or 'T1'-'T4'; NULL in early eras
    till_id              NVARCHAR(32)  NULL,
    device_id            NVARCHAR(64)  NULL,        -- 'web-xxxxxxxx' / 'app-xxxxxxxx'; NULL in early eras
    currency_code        CHAR(3)       NOT NULL REFERENCES dbo.currencies(currency_code),
    subtotal             DECIMAL(12,2) NOT NULL,
    tax_total            DECIMAL(12,2) NOT NULL,
    discount_total       DECIMAL(12,2) NOT NULL,
    total                DECIMAL(12,2) NOT NULL,
    -- receipt identity: total = subtotal - discount_total + tax_total.
    -- trade_in_offset is NOT part of total — it is the portion of total
    -- settled by part-exchange credit, so SUM(payments.amount) =
    -- total - trade_in_offset for every transaction (a receipt fully
    -- settled by trade-in has no payments row).
    trade_in_offset      DECIMAL(12,2) NOT NULL DEFAULT 0,
    -- set on a refund/return receipt, pointing at the original sale. A return
    -- is a separate transaction with NEGATIVE subtotal / tax / total (and a
    -- negative payment refunding the original tender), so net sales =
    -- SUM(total) already nets out returns. NULL on a normal forward sale.
    original_transaction_id BIGINT     NULL REFERENCES dbo.transactions(transaction_id),
    source_system        NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system),
    CONSTRAINT ck_tx_channel CHECK (channel IN (
        'in_store','phone','online','mobile_app','click_and_collect'
    )),
    CONSTRAINT ck_tx_precision CHECK (occurred_at_precision IN (
        'year','month','day','hour','minute','second','millisecond'
    ))
);
GO

CREATE TABLE dbo.transaction_lines (
    transaction_line_id BIGINT        NOT NULL PRIMARY KEY,
    transaction_id      BIGINT        NOT NULL REFERENCES dbo.transactions(transaction_id),
    line_number         SMALLINT      NOT NULL,
    release_id          BIGINT        NULL REFERENCES dbo.releases(release_id),
    hardware_id         INT           NULL REFERENCES dbo.hardware(hardware_id),  -- console/hardware line
    condition           NVARCHAR(16)  NULL,       -- 'new'/'used_*' for software OR hardware; NULL only for description-only
    description         NVARCHAR(200) NULL,       -- free-text fallback
    quantity            INT           NOT NULL,
    unit_price          DECIMAL(12,2) NOT NULL,
    line_discount       DECIMAL(12,2) NOT NULL DEFAULT 0,
    line_tax            DECIMAL(12,2) NOT NULL DEFAULT 0,
    line_total          DECIMAL(12,2) NOT NULL,
    -- a line is a software SKU XOR a hardware model (or neither: description-only)
    CONSTRAINT ck_txl_sku_xor CHECK (
        (release_id IS NOT NULL AND hardware_id IS NULL) OR
        (release_id IS NULL AND hardware_id IS NOT NULL) OR
        (release_id IS NULL AND hardware_id IS NULL)
    )
);
GO

CREATE TABLE dbo.payments (
    payment_id       BIGINT         NOT NULL PRIMARY KEY,
    transaction_id   BIGINT         NOT NULL REFERENCES dbo.transactions(transaction_id),
    method           NVARCHAR(32)   NOT NULL REFERENCES dbo.payment_methods(method),
    currency_code    CHAR(3)        NOT NULL REFERENCES dbo.currencies(currency_code),
    amount           DECIMAL(12,2)  NOT NULL,
    saved_payment_id BIGINT         NULL REFERENCES dbo.saved_payment_methods(saved_payment_id),
    processor_ref    NVARCHAR(64)   NULL
);
GO

-- trade-ins: reverse money flow (store pays customer or grants store credit);
-- tied to a transaction when part of a blended sale + trade-in
CREATE TABLE dbo.trade_ins (
    trade_in_id       BIGINT        NOT NULL PRIMARY KEY,
    occurred_at       DATETIME2(3)  NOT NULL,
    occurred_at_precision NVARCHAR(12) NOT NULL,
    shop_id           BIGINT        NULL REFERENCES dbo.shops(shop_id),
    customer_id       BIGINT        NULL REFERENCES dbo.customers(customer_id),
    staff_id          BIGINT        NULL,
    transaction_id    BIGINT        NULL REFERENCES dbo.transactions(transaction_id),  -- linked sale (blended trade-in + purchase)
    currency_code     CHAR(3)       NOT NULL REFERENCES dbo.currencies(currency_code),
    total_value       DECIMAL(12,2) NOT NULL,
    -- store_credit payouts grant to dbo.store_credit_ledger and set
    -- trade_in_offset = 0 on the linked sale (see ledger DDL)
    payout_method     NVARCHAR(16)  NOT NULL,   -- 'cash'|'store_credit'|'bank_transfer'
    source_system     NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system),
    CONSTRAINT ck_trade_ins_payout CHECK (payout_method IN ('cash','store_credit','bank_transfer'))
);
GO

CREATE TABLE dbo.trade_in_items (
    trade_in_item_id BIGINT        NOT NULL PRIMARY KEY,
    trade_in_id      BIGINT        NOT NULL REFERENCES dbo.trade_ins(trade_in_id),
    release_id       BIGINT        NULL REFERENCES dbo.releases(release_id),
    hardware_id      INT           NULL REFERENCES dbo.hardware(hardware_id),  -- traded-in console
    condition        NVARCHAR(16)  NOT NULL,
    valuation        DECIMAL(12,2) NOT NULL,
    notes            NVARCHAR(200) NULL,
    CONSTRAINT ck_trade_in_item_cond CHECK (condition IN (
        'used_mint','used_good','used_fair','used_loose'
    ))
);
GO

-- store credit is shop-local. 'credit_granted' rows (+amount) carry both
-- trade_in_id and transaction_id; 'credit_used' rows (-amount) carry
-- transaction_id only. Trade-ins paid out as store_credit set
-- trade_in_offset = 0 on the linked transaction (value flows through this
-- ledger instead, never both).
CREATE TABLE dbo.store_credit_ledger (
    ledger_id         BIGINT        NOT NULL PRIMARY KEY,
    customer_id       BIGINT        NOT NULL REFERENCES dbo.customers(customer_id),
    occurred_at       DATETIME2(3)  NOT NULL,
    event_type        NVARCHAR(24)  NOT NULL,   -- 'credit_granted'|'credit_used'|'credit_expired'|'credit_adjusted'
    amount            DECIMAL(12,2) NOT NULL,   -- signed; positive = grant, negative = redeem
    currency_code     CHAR(3)       NOT NULL REFERENCES dbo.currencies(currency_code),
    trade_in_id       BIGINT        NULL,
    transaction_id    BIGINT        NULL,
    source_system     NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system)
);
GO

-- Live-era ID generation: sequences start at 10^15 so live-insert rows never collide with bulk-loaded ids
CREATE SEQUENCE dbo.seq_transactions              START WITH 1000000000000000 INCREMENT BY 1 CACHE 100;
CREATE SEQUENCE dbo.seq_transaction_lines         START WITH 1000000000000000 INCREMENT BY 1 CACHE 100;
CREATE SEQUENCE dbo.seq_payments                  START WITH 1000000000000000 INCREMENT BY 1 CACHE 100;
CREATE SEQUENCE dbo.seq_inventory_movements       START WITH 1000000000000000 INCREMENT BY 1 CACHE 100;
CREATE SEQUENCE dbo.seq_trade_ins                 START WITH 1000000000000000 INCREMENT BY 1 CACHE 100;
CREATE SEQUENCE dbo.seq_trade_in_items            START WITH 1000000000000000 INCREMENT BY 1 CACHE 100;
CREATE SEQUENCE dbo.seq_store_credit_ledger       START WITH 1000000000000000 INCREMENT BY 1 CACHE 100;
CREATE SEQUENCE dbo.seq_customers                 START WITH 1000000000000000 INCREMENT BY 1 CACHE 100;
CREATE SEQUENCE dbo.seq_customer_addresses        START WITH 1000000000000000 INCREMENT BY 1 CACHE 100;
CREATE SEQUENCE dbo.seq_customer_lifecycle_events START WITH 1000000000000000 INCREMENT BY 1 CACHE 100;
GO

-- hr — human resources

-- person is the immutable identity; employment history lives on employment_spells
CREATE TABLE hr.persons (
    person_id         BIGINT        NOT NULL PRIMARY KEY,
    first_name        NVARCHAR(100) NULL,
    last_name         NVARCHAR(100) NULL,
    date_of_birth     DATE          NULL,
    national_id_hash  NVARCHAR(64)  NULL,       -- hash only; raw ID never stored
    country_of_residence CHAR(2)    NULL REFERENCES dbo.countries(country_code),
    created_at        DATETIME2(3)  NOT NULL,
    anonymised_at     DATETIME2(3)  NULL,
    source_system     NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system)
);

CREATE TABLE hr.departments (
    department_id     INT           NOT NULL PRIMARY KEY,
    code              NVARCHAR(16)  NOT NULL UNIQUE,
    name              NVARCHAR(100) NOT NULL,
    parent_department_id INT        NULL REFERENCES hr.departments(department_id)
);

CREATE TABLE hr.roles (
    role_id           INT           NOT NULL PRIMARY KEY,
    code              NVARCHAR(32)  NOT NULL UNIQUE,
    name              NVARCHAR(100) NOT NULL,
    is_retail_staff   BIT           NOT NULL DEFAULT 0  -- can ring up sales?
);

CREATE TABLE hr.pay_grades (
    pay_grade_id      INT           NOT NULL PRIMARY KEY,
    code              NVARCHAR(16)  NOT NULL UNIQUE,
    description       NVARCHAR(100) NOT NULL
);

-- employment spell — one person can have many; payroll / reviews reference a spell, not a person
CREATE TABLE hr.employment_spells (
    spell_id          BIGINT        NOT NULL PRIMARY KEY,
    person_id         BIGINT        NOT NULL REFERENCES hr.persons(person_id),
    role_id           INT           NOT NULL REFERENCES hr.roles(role_id),
    department_id     INT           NOT NULL REFERENCES hr.departments(department_id),
    pay_grade_id      INT           NULL REFERENCES hr.pay_grades(pay_grade_id),
    home_shop_id      BIGINT        NULL REFERENCES dbo.shops(shop_id),
    started_at        DATE          NOT NULL,
    ended_at          DATE          NULL,
    termination_reason NVARCHAR(64) NULL,
    source_system     NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system)
);

-- declare the staff_id FK now that hr.employment_spells exists (schema ordering)
ALTER TABLE dbo.transactions
    ADD CONSTRAINT fk_tx_staff
    FOREIGN KEY (staff_id) REFERENCES hr.employment_spells(spell_id);
ALTER TABLE dbo.trade_ins
    ADD CONSTRAINT fk_trade_ins_staff
    FOREIGN KEY (staff_id) REFERENCES hr.employment_spells(spell_id);
GO

CREATE TABLE hr.contracts (
    contract_id       BIGINT        NOT NULL PRIMARY KEY,
    spell_id          BIGINT        NOT NULL REFERENCES hr.employment_spells(spell_id),
    contract_type     NVARCHAR(32)  NOT NULL,   -- 'permanent_full'|'permanent_part'|'fixed_term'|'temp'|'intern'
    weekly_hours      DECIMAL(5,2)  NULL,
    signed_at         DATE          NOT NULL,
    source_system     NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system),
    CONSTRAINT ck_contract_type CHECK (contract_type IN (
        'permanent_full','permanent_part','fixed_term','temp','intern'
    ))
);

CREATE TABLE hr.staff_addresses (
    staff_address_id  BIGINT        NOT NULL PRIMARY KEY,
    person_id         BIGINT        NOT NULL REFERENCES hr.persons(person_id),
    effective_from    DATE          NOT NULL,
    effective_to      DATE          NULL,
    line1             NVARCHAR(200) NULL,
    line2             NVARCHAR(200) NULL,
    city              NVARCHAR(500) NULL,       -- GeoNames CA FSA entries can be verbose
    region            NVARCHAR(500) NULL,
    postal_code       NVARCHAR(20)  NULL,
    country_code      CHAR(2)       NOT NULL REFERENCES dbo.countries(country_code),
    anonymised_at     DATETIME2(3)  NULL
);

CREATE TABLE hr.staff_shifts (
    shift_id          BIGINT        NOT NULL PRIMARY KEY,
    spell_id          BIGINT        NOT NULL REFERENCES hr.employment_spells(spell_id),
    shop_id           BIGINT        NOT NULL REFERENCES dbo.shops(shop_id),
    shift_start       DATETIME2(3)  NOT NULL,
    shift_end         DATETIME2(3)  NOT NULL,
    break_minutes     INT           NOT NULL DEFAULT 0,
    source_system     NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system)
);

CREATE TABLE hr.payroll_runs (
    payroll_run_id    BIGINT        NOT NULL PRIMARY KEY,
    country_code      CHAR(2)       NOT NULL REFERENCES dbo.countries(country_code),
    period_start      DATE          NOT NULL,
    period_end        DATE          NOT NULL,
    paid_at           DATE          NOT NULL,
    currency_code     CHAR(3)       NOT NULL REFERENCES dbo.currencies(currency_code),
    status            NVARCHAR(16)  NOT NULL DEFAULT 'posted',  -- 'draft'|'posted'|'void'
    source_system     NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system)
);

CREATE TABLE hr.payroll_lines (
    payroll_line_id   BIGINT        NOT NULL PRIMARY KEY,
    payroll_run_id    BIGINT        NOT NULL REFERENCES hr.payroll_runs(payroll_run_id),
    spell_id          BIGINT        NOT NULL REFERENCES hr.employment_spells(spell_id),
    gross             DECIMAL(12,2) NOT NULL,
    tax               DECIMAL(12,2) NOT NULL,
    employee_contribs DECIMAL(12,2) NOT NULL DEFAULT 0,
    employer_contribs DECIMAL(12,2) NOT NULL DEFAULT 0,
    net               DECIMAL(12,2) NOT NULL
);
GO

-- Seed reference data

INSERT INTO dbo.source_systems (source_system, nature, effective_from, effective_to, description) VALUES
    ('pos_legacy_1986_2003',       'migrated', '1986-01-01', '2003-12-31', 'AS/400 era POS; nightly batch aggregates for most of its life'),
    ('pos_transitional_2004_2015', 'migrated', '2004-01-01', '2015-12-31', 'Windows-Server SQL era POS; timestamp precision improves mid-life'),
    ('pos_current',                'native',   '2016-01-01', NULL,         'Modern POS, full fidelity, live writes'),
    ('phone_orders_1995_2010',     'migrated', '1995-06-01', '2010-03-31', 'Mainframe phone-sales system; after 2010-03 phone orders were keyed directly into the POS'),
    ('web_legacy_2001_2007',       'migrated', '2001-03-01', '2007-09-30', 'First-gen storefront, incomplete event capture'),
    ('web_2008_plus',              'native',   '2007-10-01', NULL,         'Current web stack'),
    ('mobile_app_2010_plus',       'native',   '2010-04-01', NULL,         'iOS / Android apps'),
    ('hr_paper_1986_1998',         'migrated', '1986-01-01', '1998-12-31', 'Paper-based HR; senior employees digitised retrospectively'),
    ('hr_legacy_hris_1999_2013',   'migrated', '1999-01-01', '2013-12-31', 'First HRIS; limited non-HQ coverage'),
    ('hr_unified_2014_plus',       'native',   '2014-01-01', NULL,         'Current cloud HRIS');

INSERT INTO dbo.privacy_regimes (regime, effective_from, retention_days, deletion_sla_days, description) VALUES
    ('none',   '1900-01-01',  NULL,  NULL, 'No specific regime applies'),
    ('pipeda', '2000-01-01',  NULL,  30,   'Canadian privacy legislation, 2000'),
    ('appi',   '2005-04-01',  NULL,  30,   'Japanese Act on the Protection of Personal Information'),
    ('gdpr',   '2018-05-25',  NULL,  30,   'EU General Data Protection Regulation'),
    ('ccpa',   '2020-01-01',  NULL,  45,   'California Consumer Privacy Act'),
    ('lgpd',   '2020-08-16',  NULL,  15,   'Brazilian Lei Geral de Proteção de Dados'),
    ('uk_gdpr','2021-01-01',  NULL,  30,   'UK GDPR post-Brexit divergence');

INSERT INTO dbo.payment_methods (method, introduced, retired, channel_scope, description) VALUES
    ('cash',             '1986-01-01', NULL,         NULL,          'Physical cash'),
    ('check',            '1986-01-01', '2010-12-31', NULL,          'Personal check'),
    ('card_manual',      '1986-01-01', '1998-12-31', NULL,          'Carbon-copy imprint card'),
    ('card_magstripe',        '1992-01-01', '2018-12-31', NULL,          'Magnetic-stripe swipe card (pre-EMV)'),
    ('card_emv',              '2006-01-01', NULL, NULL,          'EMV chip-and-PIN card'),
    ('card_contactless',      '2011-01-01', NULL, NULL,          'Contactless card'),
    ('mobile_wallet_ios',     '2015-01-01', NULL, NULL,          'iOS mobile wallet (NFC via iPhone / Apple Watch)'),
    ('mobile_wallet_android', '2016-01-01', NULL, NULL,          'Android mobile wallet (NFC via Android phone / Wear OS)'),
    ('third_party_online',    '2001-01-01', NULL, 'online_only', 'Third-party online processor (PayPal-class rail)'),
    ('bnpl',                  '2017-01-01', NULL, 'online_only', 'Buy-now-pay-later (Klarna-class rail)'),
    ('gift_card',             '2004-01-01', NULL, NULL,          'Store gift card redemption'),
    ('store_credit',     '1986-01-01', NULL,         NULL,          'Customer store credit, usually from trade-ins');

INSERT INTO dbo.currencies (currency_code, name, minor_unit) VALUES
    ('USD','US Dollar',2),('EUR','Euro',2),('GBP','Pound Sterling',2),('JPY','Japanese Yen',0),
    ('AUD','Australian Dollar',2),('CAD','Canadian Dollar',2),('BRL','Brazilian Real',2),
    ('KRW','South Korean Won',0),('SEK','Swedish Krona',2),('NOK','Norwegian Krone',2),
    ('DKK','Danish Krone',2),('CHF','Swiss Franc',2),('PLN','Polish Zloty',2),('CZK','Czech Koruna',2);

-- FX rates against USD — five-year snapshots 1990-2025 (approximate annual averages)
INSERT INTO dbo.fx_rates (currency_code, effective_year, rate_to_usd) VALUES
    -- 1990 (pre-Euro; BRL hyperinflation era so omitted)
    ('USD',1990,1.000000),('GBP',1990,0.563000),('JPY',1990,144.800000),
    ('AUD',1990,1.281000),('CAD',1990,1.167000),('CHF',1990,1.389000),
    ('SEK',1990,5.918000),('NOK',1990,6.260000),('DKK',1990,6.185000),
    ('KRW',1990,707.760000),
    -- 1995 (PLN redenominated; CZK active; BRL real launched 1994)
    ('USD',1995,1.000000),('GBP',1995,0.634000),('JPY',1995,94.060000),
    ('AUD',1995,1.348000),('CAD',1995,1.372000),('CHF',1995,1.182000),
    ('SEK',1995,7.133000),('NOK',1995,6.337000),('DKK',1995,5.602000),
    ('KRW',1995,771.270000),('BRL',1995,0.920000),
    ('PLN',1995,2.425000),('CZK',1995,26.541000),
    -- 2000 (EUR launched 1999, electronic only until 2002)
    ('USD',2000,1.000000),('EUR',2000,1.085000),('GBP',2000,0.659000),
    ('JPY',2000,107.770000),('AUD',2000,1.724000),('CAD',2000,1.485000),
    ('CHF',2000,1.689000),('SEK',2000,9.162000),('NOK',2000,8.802000),
    ('DKK',2000,8.083000),('KRW',2000,1130.900000),('BRL',2000,1.829000),
    ('PLN',2000,4.346000),('CZK',2000,38.598000),
    -- 2005
    ('USD',2005,1.000000),('EUR',2005,0.804000),('GBP',2005,0.550000),
    ('JPY',2005,110.110000),('AUD',2005,1.310000),('CAD',2005,1.211000),
    ('CHF',2005,1.245000),('SEK',2005,7.471000),('NOK',2005,6.441000),
    ('DKK',2005,5.996000),('KRW',2005,1024.270000),('BRL',2005,2.434000),
    ('PLN',2005,3.234000),('CZK',2005,23.957000),
    -- 2010
    ('USD',2010,1.000000),('EUR',2010,0.755000),('GBP',2010,0.647000),
    ('JPY',2010,87.780000),('AUD',2010,1.089000),('CAD',2010,1.030000),
    ('CHF',2010,1.043000),('SEK',2010,7.207000),('NOK',2010,6.045000),
    ('DKK',2010,5.624000),('KRW',2010,1156.060000),('BRL',2010,1.760000),
    ('PLN',2010,3.018000),('CZK',2010,19.111000),
    -- 2015
    ('USD',2015,1.000000),('EUR',2015,0.902000),('GBP',2015,0.655000),
    ('JPY',2015,121.050000),('AUD',2015,1.331000),('CAD',2015,1.279000),
    ('CHF',2015,0.962000),('SEK',2015,8.435000),('NOK',2015,8.069000),
    ('DKK',2015,6.731000),('KRW',2015,1131.500000),('BRL',2015,3.339000),
    ('PLN',2015,3.776000),('CZK',2015,24.595000),
    -- 2020
    ('USD',2020,1.000000),('EUR',2020,0.876000),('GBP',2020,0.780000),
    ('JPY',2020,106.770000),('AUD',2020,1.453000),('CAD',2020,1.341000),
    ('CHF',2020,0.939000),('SEK',2020,9.211000),('NOK',2020,9.418000),
    ('DKK',2020,6.539000),('KRW',2020,1180.270000),('BRL',2020,5.155000),
    ('PLN',2020,3.900000),('CZK',2020,23.213000),
    -- 2025 (current approx)
    ('USD',2025,1.000000),('EUR',2025,0.930000),('GBP',2025,0.790000),
    ('JPY',2025,150.000000),('AUD',2025,1.520000),('CAD',2025,1.360000),
    ('CHF',2025,0.880000),('SEK',2025,10.550000),('NOK',2025,10.740000),
    ('DKK',2025,6.900000),('KRW',2025,1380.000000),('BRL',2025,5.400000),
    ('PLN',2025,3.990000),('CZK',2025,23.300000);

INSERT INTO dbo.countries (country_code, name, default_currency, governing_regime) VALUES
    ('US','United States','USD','ccpa'),
    ('GB','United Kingdom','GBP','uk_gdpr'),
    ('DE','Germany','EUR','gdpr'),
    ('FR','France','EUR','gdpr'),
    ('ES','Spain','EUR','gdpr'),
    ('IT','Italy','EUR','gdpr'),
    ('NL','Netherlands','EUR','gdpr'),
    ('JP','Japan','JPY','appi'),
    ('AU','Australia','AUD','none'),
    ('CA','Canada','CAD','pipeda'),
    ('BR','Brazil','BRL','lgpd'),
    ('KR','South Korea','KRW','none'),
    ('SE','Sweden','SEK','gdpr'),
    ('NO','Norway','NOK','none'),
    ('DK','Denmark','DKK','gdpr'),
    ('CH','Switzerland','CHF','none'),
    ('PL','Poland','PLN','gdpr'),
    ('CZ','Czech Republic','CZK','gdpr');

-- platforms table left empty here; populated by the catalog-load step from dbo.releases distinct platform values
GO

-- Additional CHECK and range constraints (applied before load)

-- Missing CHECK constraints on enumerated string columns

ALTER TABLE dbo.inventory_movements
    ADD CONSTRAINT ck_inv_movement_type CHECK (movement_type IN (
        'sale','trade_in','delivery','transfer_in','transfer_out','adjustment','return'
    ));

ALTER TABLE dbo.inventory_movements
    ADD CONSTRAINT ck_inv_reference_type CHECK (
        reference_type IS NULL OR reference_type IN ('transaction','trade_in')
    );

ALTER TABLE dbo.customer_lifecycle_events
    ADD CONSTRAINT ck_lifecycle_event_type CHECK (event_type IN (
        'signup','reactivated','marked_dormant','anonymisation_requested','anonymised','deleted'
    ));

ALTER TABLE dbo.consent_events
    ADD CONSTRAINT ck_consent_channel CHECK (channel IN ('email','sms','push','post'));

ALTER TABLE dbo.loyalty_memberships
    ADD CONSTRAINT ck_loyalty_scheme CHECK (scheme IN (
        'stamp_1986','card_1998','unified_2011'
    ));

ALTER TABLE dbo.loyalty_memberships
    ADD CONSTRAINT ck_loyalty_tier CHECK (
        tier IS NULL OR tier IN ('bronze','silver','gold','platinum')
    );

ALTER TABLE dbo.store_credit_ledger
    ADD CONSTRAINT ck_store_credit_event_type CHECK (event_type IN (
        'credit_granted','credit_used','credit_expired','credit_adjusted'
    ));

ALTER TABLE hr.payroll_runs
    ADD CONSTRAINT ck_payroll_runs_status CHECK (status IN ('draft','posted','void'));

-- Range / date-pair CHECKs

ALTER TABLE dbo.shops
    ADD CONSTRAINT ck_shops_dates CHECK (
        closed_date IS NULL OR closed_date >= opened_date
    );

ALTER TABLE dbo.customer_addresses
    ADD CONSTRAINT ck_cust_addr_dates CHECK (
        effective_to IS NULL OR effective_to >= effective_from
    );

ALTER TABLE dbo.loyalty_memberships
    ADD CONSTRAINT ck_loyalty_dates CHECK (
        closed_at IS NULL OR closed_at >= enrolled_at
    );

ALTER TABLE dbo.saved_payment_methods
    ADD CONSTRAINT ck_saved_payment_dates CHECK (
        removed_at IS NULL OR removed_at >= added_at
    );

ALTER TABLE dbo.source_systems
    ADD CONSTRAINT ck_source_systems_dates CHECK (
        effective_to IS NULL OR effective_to >= effective_from
    );

ALTER TABLE dbo.payment_methods
    ADD CONSTRAINT ck_payment_methods_dates CHECK (
        retired IS NULL OR retired >= introduced
    );

ALTER TABLE hr.employment_spells
    ADD CONSTRAINT ck_emp_spells_dates CHECK (
        ended_at IS NULL OR ended_at >= started_at
    );

ALTER TABLE hr.staff_addresses
    ADD CONSTRAINT ck_staff_addr_dates CHECK (
        effective_to IS NULL OR effective_to >= effective_from
    );

ALTER TABLE hr.staff_shifts
    ADD CONSTRAINT ck_staff_shifts_dates CHECK (shift_end > shift_start);

ALTER TABLE hr.payroll_runs
    ADD CONSTRAINT ck_payroll_runs_period CHECK (
        period_end >= period_start AND paid_at >= period_end
    );

-- Closure-era additions: shops.closure_reason, hr.compensation_history, finance.monthly_summary
ALTER TABLE dbo.shops
    ADD closure_reason NVARCHAR(40) NULL;
GO

CREATE TABLE hr.compensation_history (
    history_id      BIGINT        IDENTITY(1,1) PRIMARY KEY,
    person_id       BIGINT        NOT NULL REFERENCES hr.persons(person_id),
    effective_from  DATE          NOT NULL,
    effective_to    DATE          NULL,
    annual_wage     DECIMAL(12,2) NOT NULL,   -- in person's currency_code (joined via persons.country_of_residence → countries.currency_code)
    currency_code   CHAR(3)       NOT NULL REFERENCES dbo.currencies(currency_code),
    change_reason   NVARCHAR(40)  NOT NULL    -- hire, cola, tenure_step, promotion, retention_2014, retention_2015,
                                              -- severance_2014, severance_2015, severance_2016, winddown_premium
);
GO

ALTER TABLE hr.compensation_history
    ADD CONSTRAINT ck_comp_history_dates CHECK (
        effective_to IS NULL OR effective_to >= effective_from
    );
GO

IF SCHEMA_ID('finance') IS NULL EXEC('CREATE SCHEMA finance AUTHORIZATION dbo');
GO

CREATE TABLE finance.monthly_summary (
    year_month       CHAR(7)       NOT NULL PRIMARY KEY,  -- 'YYYY-MM'
    revenue_usd      DECIMAL(15,2) NOT NULL,
    cogs_usd         DECIMAL(15,2) NOT NULL,              -- ~65% of revenue normally
    wages_usd        DECIMAL(15,2) NOT NULL,              -- recurring wage cost (excludes one-off severance)
    severance_usd    DECIMAL(15,2) NOT NULL,              -- one-off lumps in restructuring quarters
    rent_usd         DECIMAL(15,2) NOT NULL,              -- declines as shops close
    other_opex_usd   DECIMAL(15,2) NOT NULL,
    net_income_usd   DECIMAL(15,2) NOT NULL,
    shops_active     INT           NOT NULL,
    staff_active     INT           NOT NULL,
    notes            NVARCHAR(400) NULL
);
GO

-- web — the community layer: accounts, product reviews, comment threads, votes, and clickstream
IF SCHEMA_ID('web') IS NULL EXEC('CREATE SCHEMA web AUTHORIZATION dbo');
GO

-- one account per customer; username is unique and is NOT the customer's legal
-- name. created_at is whole-second web-log precision
CREATE TABLE web.accounts (
    account_id      BIGINT        NOT NULL PRIMARY KEY,
    customer_id     BIGINT        NOT NULL REFERENCES dbo.customers(customer_id),
    username        NVARCHAR(40)  NOT NULL,
    created_at      DATETIME2(0)  NOT NULL,
    status          NVARCHAR(16)  NOT NULL DEFAULT 'active',   -- 'active'|'closed'|'banned'
    source_system   NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system),
    CONSTRAINT ux_accounts_customer UNIQUE (customer_id),
    CONSTRAINT ux_accounts_username UNIQUE (username),
    CONSTRAINT ck_accounts_status CHECK (status IN ('active','closed','banned'))
);
GO

-- Exactly one of release_id / hardware_id is set (a review targets a game OR a
-- console). is_verified_purchase is set only when the reviewer really bought the
-- item. comment_count/helpful_count/funny_count are denormalised and kept exact
-- by the loader. Rating is 1-5 stars.
CREATE TABLE web.reviews (
    review_id            BIGINT        NOT NULL PRIMARY KEY,
    account_id           BIGINT        NOT NULL REFERENCES web.accounts(account_id),
    release_id           BIGINT        NULL REFERENCES dbo.releases(release_id),
    hardware_id          INT           NULL REFERENCES dbo.hardware(hardware_id),
    rating               TINYINT       NOT NULL,
    title                NVARCHAR(200) NULL,           -- NULL = untitled
    body                 NVARCHAR(MAX) NOT NULL,
    language_code        CHAR(2)       NOT NULL,       -- 'en','ja','de','fr','pt','pl','sv','ko'
    is_verified_purchase BIT           NOT NULL,
    posted_at            DATETIME2(0)  NOT NULL,
    edited_at            DATETIME2(0)  NULL,
    comment_count        INT           NOT NULL DEFAULT 0,
    helpful_count        INT           NOT NULL DEFAULT 0,
    funny_count          INT           NOT NULL DEFAULT 0,
    source_system        NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system),
    CONSTRAINT ck_reviews_target_xor CHECK (
        (release_id IS NOT NULL AND hardware_id IS NULL) OR
        (release_id IS NULL AND hardware_id IS NOT NULL)),
    CONSTRAINT ck_reviews_rating CHECK (rating BETWEEN 1 AND 5)
);
GO

-- flat comment threads (no nesting)
CREATE TABLE web.review_comments (
    comment_id      BIGINT         NOT NULL PRIMARY KEY,
    review_id       BIGINT         NOT NULL REFERENCES web.reviews(review_id),
    account_id      BIGINT         NOT NULL REFERENCES web.accounts(account_id),
    body            NVARCHAR(2000) NOT NULL,
    posted_at       DATETIME2(0)   NOT NULL,
    source_system   NVARCHAR(48)   NOT NULL REFERENCES dbo.source_systems(source_system)
);
GO

-- one vote of each kind per (review, account)
CREATE TABLE web.review_votes (
    vote_id         BIGINT        NOT NULL PRIMARY KEY,
    review_id       BIGINT        NOT NULL REFERENCES web.reviews(review_id),
    account_id      BIGINT        NOT NULL REFERENCES web.accounts(account_id),
    vote_type       NVARCHAR(12)  NOT NULL,           -- 'helpful'|'funny'|'unhelpful'
    occurred_at     DATETIME2(0)  NOT NULL,
    CONSTRAINT ux_votes_one_per_kind UNIQUE (review_id, account_id, vote_type),
    CONSTRAINT ck_votes_type CHECK (vote_type IN ('helpful','funny','unhelpful'))
);
GO

-- web-server logs. account_id NULL = not logged in. No FK on session_id
-- (it is a log artifact, not an entity).
CREATE TABLE web.page_views (
    page_view_id      BIGINT        NOT NULL PRIMARY KEY,
    session_id        BIGINT        NOT NULL,
    account_id        BIGINT        NULL REFERENCES web.accounts(account_id),
    occurred_at       DATETIME2(0)  NOT NULL,
    url_path          NVARCHAR(200) NOT NULL,         -- '/product/41991', '/reviews/12345', '/cart'
    http_status       SMALLINT      NOT NULL,
    referrer_domain   NVARCHAR(80)  NULL,             -- 'google.com', 'gamefaqs.com', NULL = direct
    user_agent_family NVARCHAR(60)  NOT NULL,         -- 'IE6', 'Netscape4', 'Firefox2', 'Googlebot'
    client_country    CHAR(2)       NOT NULL REFERENCES dbo.countries(country_code),
    bytes_sent        INT           NOT NULL,
    source_system     NVARCHAR(48)  NOT NULL REFERENCES dbo.source_systems(source_system)
);
GO
