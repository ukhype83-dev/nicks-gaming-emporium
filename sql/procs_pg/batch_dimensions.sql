/* =============================================================
   batch — dimension refresh procedures (PostgreSQL port)
   -------------------------------------------------------------
   Each: log start -> TRUNCATE dim -> INSERT ... SELECT from OLTP ->
   log ok. READ-ONLY on OLTP; writes only dw. SCD-1 overwrite,
   idempotent. BIT flags become real booleans; the SQL Server
   date-spine (sys.all_objects cross join) becomes generate_series.
   ============================================================= */

/* ---- dim_channel (static seed) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_dim_channel()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_dim_channel','dw.dim_channel','full');
    TRUNCATE TABLE dw.dim_channel;
    INSERT INTO dw.dim_channel(channel_key,channel_name) VALUES
        (1,'in_store'),(2,'phone'),(3,'online'),(4,'mobile_app'),(5,'click_and_collect');
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run;
    RAISE;
END $$;

/* ---- dim_condition (static seed) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_dim_condition()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_dim_condition','dw.dim_condition','full');
    TRUNCATE TABLE dw.dim_condition;
    INSERT INTO dw.dim_condition(condition_key,condition_name,is_used) VALUES
        (1,'new',false),(2,'used_mint',true),(3,'used_good',true),(4,'used_fair',true),(5,'used_loose',true);
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run;
    RAISE;
END $$;

/* ---- dim_currency ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_dim_currency()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_dim_currency','dw.dim_currency','full');
    TRUNCATE TABLE dw.dim_currency;
    INSERT INTO dw.dim_currency(currency_code,name,minor_unit)
    SELECT currency_code,name,minor_unit FROM public.currencies;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run;
    RAISE;
END $$;

/* ---- dim_geography (country -> region/market via CASE) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_dim_geography()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_dim_geography','dw.dim_geography','full');
    TRUNCATE TABLE dw.dim_geography;
    INSERT INTO dw.dim_geography(country_code,country_name,region,market,default_currency,governing_regime)
    SELECT c.country_code, c.name,
        CASE
          WHEN c.country_code IN ('US','CA','MX') THEN 'North America'
          WHEN c.country_code IN ('GB','IE','FR','DE','IT','ES','PT','PL','SE','NL','BE','AT','CH','CZ','DK','NO','FI','GR','HU','RO') THEN 'Europe'
          WHEN c.country_code IN ('JP','KR','CN','HK','TW','SG') THEN 'Asia'
          WHEN c.country_code IN ('BR','AR','CL','CO') THEN 'Latin America'
          WHEN c.country_code IN ('AU','NZ') THEN 'Oceania'
          ELSE 'Other'
        END,
        CASE
          WHEN c.country_code IN ('US','CA') THEN 'NA'
          WHEN c.country_code IN ('GB','IE','FR','DE','IT','ES','PT','PL','SE','NL','BE','AT','CH','CZ','DK','NO','FI','GR','HU','RO') THEN 'EU'
          WHEN c.country_code IN ('JP','KR') THEN 'JP'
          WHEN c.country_code IN ('BR','AR','CL','CO','MX') THEN 'LATAM'
          WHEN c.country_code IN ('AU','NZ','CN','HK','TW','SG') THEN 'APAC'
          ELSE 'Other'
        END,
        c.default_currency, c.governing_regime
    FROM public.countries c;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run;
    RAISE;
END $$;

/* ---- dim_platform ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_dim_platform()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_dim_platform','dw.dim_platform','full');
    TRUNCATE TABLE dw.dim_platform;
    INSERT INTO dw.dim_platform(platform_key,platform_name,family,released_year,discontinued_year)
    SELECT platform_id,name,family,released_year,discontinued_year FROM public.platforms;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run;
    RAISE;
END $$;

/* ---- dim_product : games (release_id) UNION hardware (9e9 + hardware_id) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_dim_product()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_dim_product','dw.dim_product','full');
    TRUNCATE TABLE dw.dim_product;
    INSERT INTO dw.dim_product(product_key,product_kind,release_id,hardware_id,title,platform_key,platform_name,
                          category,publisher,developer,manufacturer,media_type,first_release_date,launch_usd)
    SELECT r.release_id, 'game', r.release_id, NULL,
           LEFT(r.title,450), r.platform_id, p.name,
           r.genre, r.publisher, r.developer, NULL, r.media_type, r.first_release_date, NULL
    FROM public.releases r LEFT JOIN public.platforms p ON p.platform_id = r.platform_id
    UNION ALL
    SELECT 9000000000 + h.hardware_id, 'hardware', NULL, h.hardware_id,
           h.model_name, h.platform_id, p.name,
           h.kind, NULL, NULL, h.manufacturer, NULL,
           COALESCE(h.release_na,h.release_jp,h.release_eu), h.launch_usd
    FROM public.hardware h LEFT JOIN public.platforms p ON p.platform_id = h.platform_id;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run;
    RAISE;
END $$;

/* ---- dim_shop ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_dim_shop()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_dim_shop','dw.dim_shop','full');
    TRUNCATE TABLE dw.dim_shop;
    WITH addr AS (
        SELECT shop_id, city, region,
               ROW_NUMBER() OVER (PARTITION BY shop_id ORDER BY shop_address_id) rn
        FROM public.shop_addresses
    )
    INSERT INTO dw.dim_shop(shop_key,shop_code,name,country_code,region,city,opened_date,closed_date,is_open,currency_code,is_flagship)
    SELECT s.shop_id, s.shop_code, s.name, s.country_code, g.region, a.city,
           s.opened_date, s.closed_date, (s.closed_date IS NULL),
           s.currency_code, (s.shop_id IN (1,2))
    FROM public.shops s
    LEFT JOIN addr a ON a.shop_id = s.shop_id AND a.rn = 1
    LEFT JOIN dw.dim_geography g ON g.country_code = s.country_code;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run;
    RAISE;
END $$;

/* ---- dim_staff (grain = employment spell) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_dim_staff()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_dim_staff','dw.dim_staff','full');
    TRUNCATE TABLE dw.dim_staff;
    INSERT INTO dw.dim_staff(staff_key,person_id,full_name,role_title,home_shop_id,started_on,ended_on)
    SELECT es.spell_id, es.person_id,
           TRIM(COALESCE(pe.first_name,'') || ' ' || COALESCE(pe.last_name,'')),
           ro.name, es.home_shop_id, es.started_at, es.ended_at
    FROM hr.employment_spells es
    LEFT JOIN hr.persons pe ON pe.person_id = es.person_id
    LEFT JOIN hr.roles ro ON ro.role_id = es.role_id;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run;
    RAISE;
END $$;

/* ---- dim_date : 1986-01-01 -> p_end, via generate_series ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_dim_date(p_start date DEFAULT '1986-01-01', p_end date DEFAULT '2016-12-31')
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_dim_date','dw.dim_date','full');
    TRUNCATE TABLE dw.dim_date;
    INSERT INTO dw.dim_date(date_key,full_date,year,quarter,month,month_name,week_of_year,day_of_month,
                       day_of_week,day_name,is_weekend,is_holiday_season,era,is_fire_sale)
    SELECT to_char(d,'YYYYMMDD')::int, d::date,
           extract(year from d)::smallint, extract(quarter from d)::smallint, extract(month from d)::smallint,
           to_char(d,'FMMonth'), extract(week from d)::smallint, extract(day from d)::smallint,
           extract(isodow from d)::smallint,                    -- 1=Mon..7=Sun
           to_char(d,'FMDay'),
           (extract(isodow from d) IN (6,7)),
           (extract(month from d) IN (11,12)),
           CASE
             WHEN d <= DATE '1991-12-31' THEN 'founding'
             WHEN d <= DATE '2003-12-31' THEN 'growth'
             WHEN d <= DATE '2010-12-31' THEN 'peak'
             WHEN d <  DATE '2016-04-02' THEN 'decline'
             WHEN d <= DATE '2016-09-30' THEN 'fire_sale'
             ELSE 'closed'
           END,
           (d >= DATE '2016-04-02' AND d <= DATE '2016-09-30')
    FROM generate_series(p_start::timestamp, p_end::timestamp, interval '1 day') AS g(d);
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run;
    RAISE;
END $$;

/* ---- dim_customer (~39.4M; set-based address/loyalty pick via ROW_NUMBER) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_dim_customer()
LANGUAGE plpgsql AS $$
DECLARE v_run bigint; v_rows bigint;
BEGIN
    v_run := batch.log_start('batch.usp_refresh_dim_customer','dw.dim_customer','full');
    TRUNCATE TABLE dw.dim_customer;
    WITH addr AS (
        SELECT customer_id, country_code, city, region,
               ROW_NUMBER() OVER (PARTITION BY customer_id ORDER BY
                    CASE WHEN effective_to IS NULL THEN 0 ELSE 1 END,
                    CASE WHEN address_type='billing' THEN 0 ELSE 1 END,
                    effective_from DESC) rn
        FROM public.customer_addresses
    ),
    loy AS (
        SELECT customer_id, tier,
               ROW_NUMBER() OVER (PARTITION BY customer_id ORDER BY
                    CASE WHEN closed_at IS NULL THEN 0 ELSE 1 END, enrolled_at DESC) rn
        FROM public.loyalty_memberships WHERE tier IS NOT NULL
    )
    INSERT INTO dw.dim_customer(customer_key,status,signup_date,signup_year,signup_cohort,country_code,region,city,
                           loyalty_tier,is_anonymised,birth_year,age_band)
    SELECT c.customer_id, c.status, c.signed_up_at::date, extract(year from c.signed_up_at)::smallint,
           to_char(c.signed_up_at,'YYYY-MM'), a.country_code, g.region, a.city,
           l.tier,
           (c.anonymised_at IS NOT NULL OR c.status='anonymised'),
           extract(year from c.date_of_birth)::smallint,
           CASE WHEN c.date_of_birth IS NULL THEN NULL
                WHEN 2016 - extract(year from c.date_of_birth) < 18 THEN '<18'
                WHEN 2016 - extract(year from c.date_of_birth) <= 24 THEN '18-24'
                WHEN 2016 - extract(year from c.date_of_birth) <= 34 THEN '25-34'
                WHEN 2016 - extract(year from c.date_of_birth) <= 44 THEN '35-44'
                WHEN 2016 - extract(year from c.date_of_birth) <= 54 THEN '45-54'
                ELSE '55+' END
    FROM public.customers c
    LEFT JOIN addr a ON a.customer_id = c.customer_id AND a.rn = 1
    LEFT JOIN loy  l ON l.customer_id = c.customer_id AND l.rn = 1
    LEFT JOIN dw.dim_geography g ON g.country_code = a.country_code;
    GET DIAGNOSTICS v_rows = ROW_COUNT;
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', rows_affected=v_rows, status='ok' WHERE run_id=v_run;
EXCEPTION WHEN OTHERS THEN
    UPDATE dw.etl_run_log SET finished_at=now() AT TIME ZONE 'UTC', status='error', message=SQLERRM WHERE run_id=v_run;
    RAISE;
END $$;

/* ---- orchestrator: refresh every dimension in dependency order
   (geography before customer/shop; currency/platform independent). ---- */
CREATE OR REPLACE PROCEDURE batch.usp_refresh_all_dimensions()
LANGUAGE plpgsql AS $$
BEGIN
    CALL batch.usp_refresh_dim_channel();
    CALL batch.usp_refresh_dim_condition();
    CALL batch.usp_refresh_dim_currency();
    CALL batch.usp_refresh_dim_fx();          -- dense as-of FX (facts divide by this)
    CALL batch.usp_refresh_dim_geography();   -- customer/shop region join
    CALL batch.usp_refresh_dim_platform();
    CALL batch.usp_refresh_dim_date();
    CALL batch.usp_refresh_dim_product();
    CALL batch.usp_refresh_dim_shop();
    CALL batch.usp_refresh_dim_staff();
    CALL batch.usp_refresh_dim_customer();
END $$;
