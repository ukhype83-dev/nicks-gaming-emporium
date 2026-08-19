-- dim_channel (static seed)
CREATE OR ALTER PROCEDURE batch.usp_refresh_dim_channel
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_dim_channel',N'dw.dim_channel',N'full');
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.dim_channel;
        INSERT dw.dim_channel(channel_key,channel_name) VALUES
            (1,N'in_store'),(2,N'phone'),(3,N'online'),(4,N'mobile_app'),(5,N'click_and_collect');
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- dim_condition (static seed)
CREATE OR ALTER PROCEDURE batch.usp_refresh_dim_condition
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_dim_condition',N'dw.dim_condition',N'full');
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.dim_condition;
        INSERT dw.dim_condition(condition_key,condition_name,is_used) VALUES
            (1,N'new',0),(2,N'used_mint',1),(3,N'used_good',1),(4,N'used_fair',1),(5,N'used_loose',1);
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- dim_currency
CREATE OR ALTER PROCEDURE batch.usp_refresh_dim_currency
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_dim_currency',N'dw.dim_currency',N'full');
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.dim_currency;
        INSERT dw.dim_currency(currency_code,name,minor_unit)
        SELECT currency_code,name,minor_unit FROM dbo.currencies;
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- dim_geography: country → region/market via CASE
CREATE OR ALTER PROCEDURE batch.usp_refresh_dim_geography
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_dim_geography',N'dw.dim_geography',N'full');
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.dim_geography;
        INSERT dw.dim_geography(country_code,country_name,region,market,default_currency,governing_regime)
        SELECT c.country_code, c.name,
            CASE
              WHEN c.country_code IN ('US','CA','MX') THEN N'North America'
              WHEN c.country_code IN ('GB','IE','FR','DE','IT','ES','PT','PL','SE','NL','BE','AT','CH','CZ','DK','NO','FI','GR','HU','RO') THEN N'Europe'
              WHEN c.country_code IN ('JP','KR','CN','HK','TW','SG') THEN N'Asia'
              WHEN c.country_code IN ('BR','AR','CL','CO') THEN N'Latin America'
              WHEN c.country_code IN ('AU','NZ') THEN N'Oceania'
              ELSE N'Other'
            END,
            CASE
              WHEN c.country_code IN ('US','CA') THEN N'NA'
              WHEN c.country_code IN ('GB','IE','FR','DE','IT','ES','PT','PL','SE','NL','BE','AT','CH','CZ','DK','NO','FI','GR','HU','RO') THEN N'EU'
              WHEN c.country_code IN ('JP','KR') THEN N'JP'
              WHEN c.country_code IN ('BR','AR','CL','CO','MX') THEN N'LATAM'
              WHEN c.country_code IN ('AU','NZ','CN','HK','TW','SG') THEN N'APAC'
              ELSE N'Other'
            END,
            c.default_currency, c.governing_regime
        FROM dbo.countries c;
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- dim_platform
CREATE OR ALTER PROCEDURE batch.usp_refresh_dim_platform
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_dim_platform',N'dw.dim_platform',N'full');
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.dim_platform;
        INSERT dw.dim_platform(platform_key,platform_name,family,released_year,discontinued_year)
        SELECT platform_id,name,family,released_year,discontinued_year FROM dbo.platforms;
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- dim_product: games (release_id) UNION hardware (9e9 + hardware_id)
CREATE OR ALTER PROCEDURE batch.usp_refresh_dim_product
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT, @hw_offset BIGINT = 9000000000;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_dim_product',N'dw.dim_product',N'full');
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.dim_product;
        INSERT dw.dim_product(product_key,product_kind,release_id,hardware_id,title,platform_key,platform_name,
                              category,publisher,developer,manufacturer,media_type,first_release_date,launch_usd)
        SELECT r.release_id, N'game', r.release_id, NULL,
               CAST(LEFT(r.title,450) AS NVARCHAR(450)), r.platform_id, p.name,
               r.genre, r.publisher, r.developer, NULL, r.media_type, r.first_release_date, NULL
        FROM dbo.releases r LEFT JOIN dbo.platforms p ON p.platform_id = r.platform_id
        UNION ALL
        SELECT @hw_offset + h.hardware_id, N'hardware', NULL, h.hardware_id,
               h.model_name, h.platform_id, p.name,
               h.kind, NULL, NULL, h.manufacturer, NULL,
               COALESCE(h.release_na,h.release_jp,h.release_eu), h.launch_usd
        FROM dbo.hardware h LEFT JOIN dbo.platforms p ON p.platform_id = h.platform_id;
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- dim_shop
CREATE OR ALTER PROCEDURE batch.usp_refresh_dim_shop
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_dim_shop',N'dw.dim_shop',N'full');
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.dim_shop;
        ;WITH addr AS (
            SELECT shop_id, city, region,
                   ROW_NUMBER() OVER (PARTITION BY shop_id ORDER BY shop_address_id) rn
            FROM dbo.shop_addresses
        )
        INSERT dw.dim_shop(shop_key,shop_code,name,country_code,region,city,opened_date,closed_date,is_open,currency_code,is_flagship)
        SELECT s.shop_id, s.shop_code, s.name, s.country_code, g.region, a.city,
               s.opened_date, s.closed_date, CASE WHEN s.closed_date IS NULL THEN 1 ELSE 0 END,
               s.currency_code, CASE WHEN s.shop_id IN (1,2) THEN 1 ELSE 0 END
        FROM dbo.shops s
        LEFT JOIN addr a ON a.shop_id = s.shop_id AND a.rn = 1
        LEFT JOIN dw.dim_geography g ON g.country_code = s.country_code;
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- dim_staff (grain = employment spell)
CREATE OR ALTER PROCEDURE batch.usp_refresh_dim_staff
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_dim_staff',N'dw.dim_staff',N'full');
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.dim_staff;
        INSERT dw.dim_staff(staff_key,person_id,full_name,role_title,home_shop_id,started_on,ended_on)
        SELECT es.spell_id, es.person_id,
               LTRIM(RTRIM(COALESCE(pe.first_name,N'') + N' ' + COALESCE(pe.last_name,N''))),
               ro.name, es.home_shop_id, es.started_at, es.ended_at
        FROM hr.employment_spells es
        LEFT JOIN hr.persons pe ON pe.person_id = es.person_id
        LEFT JOIN hr.roles ro ON ro.role_id = es.role_id;
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- dim_date: 1986-01-01 → @end (default = business-close year end)
CREATE OR ALTER PROCEDURE batch.usp_refresh_dim_date
    @start DATE = '1986-01-01',
    @end   DATE = '2016-12-31'
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_dim_date',N'dw.dim_date',N'full');
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.dim_date;
        ;WITH days AS (
            SELECT TOP (DATEDIFF(DAY,@start,@end)+1)
                   d = DATEADD(DAY, ROW_NUMBER() OVER (ORDER BY (SELECT NULL))-1, @start)
            FROM sys.all_objects a CROSS JOIN sys.all_objects b
        )
        INSERT dw.dim_date(date_key,full_date,year,quarter,month,month_name,week_of_year,day_of_month,
                           day_of_week,day_name,is_weekend,is_holiday_season,era,is_fire_sale)
        SELECT CONVERT(INT, CONVERT(CHAR(8), d, 112)), d,
               YEAR(d), DATEPART(QUARTER,d), MONTH(d), DATENAME(MONTH,d),
               DATEPART(ISO_WEEK,d), DAY(d),
               ((DATEPART(WEEKDAY,d)+@@DATEFIRST-2)%7)+1,           -- 1=Mon..7=Sun (DATEFIRST-independent)
               DATENAME(WEEKDAY,d),
               CASE WHEN ((DATEPART(WEEKDAY,d)+@@DATEFIRST-2)%7)+1 IN (6,7) THEN 1 ELSE 0 END,
               CASE WHEN MONTH(d) IN (11,12) THEN 1 ELSE 0 END,
               CASE
                 WHEN d <= '1991-12-31' THEN N'founding'
                 WHEN d <= '2003-12-31' THEN N'growth'
                 WHEN d <= '2010-12-31' THEN N'peak'
                 WHEN d <  '2016-04-02' THEN N'decline'
                 WHEN d <= '2016-09-30' THEN N'fire_sale'
                 ELSE N'closed'
               END,
               CASE WHEN d >= '2016-04-02' AND d <= '2016-09-30' THEN 1 ELSE 0 END
        FROM days;
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- dim_customer: set-based address/loyalty pick via ROW_NUMBER
CREATE OR ALTER PROCEDURE batch.usp_refresh_dim_customer
AS
BEGIN
    SET NOCOUNT ON;
    DECLARE @run_id BIGINT;
    INSERT dw.etl_run_log(proc_name,target_object,mode) VALUES(N'batch.usp_refresh_dim_customer',N'dw.dim_customer',N'full');
    SET @run_id = SCOPE_IDENTITY();
    BEGIN TRY
        TRUNCATE TABLE dw.dim_customer;
        ;WITH addr AS (
            SELECT customer_id, country_code, city, region,
                   ROW_NUMBER() OVER (PARTITION BY customer_id ORDER BY
                        CASE WHEN effective_to IS NULL THEN 0 ELSE 1 END,
                        CASE WHEN address_type='billing' THEN 0 ELSE 1 END,
                        effective_from DESC) rn
            FROM dbo.customer_addresses
        ),
        loy AS (
            SELECT customer_id, tier,
                   ROW_NUMBER() OVER (PARTITION BY customer_id ORDER BY
                        CASE WHEN closed_at IS NULL THEN 0 ELSE 1 END, enrolled_at DESC) rn
            FROM dbo.loyalty_memberships WHERE tier IS NOT NULL
        )
        INSERT dw.dim_customer(customer_key,status,signup_date,signup_year,signup_cohort,country_code,region,city,
                               loyalty_tier,is_anonymised,birth_year,age_band)
        SELECT c.customer_id, c.status, CAST(c.signed_up_at AS DATE), YEAR(c.signed_up_at),
               CONVERT(CHAR(7), c.signed_up_at, 126), a.country_code, g.region, a.city,
               l.tier,
               CASE WHEN c.anonymised_at IS NOT NULL OR c.status='anonymised' THEN 1 ELSE 0 END,
               YEAR(c.date_of_birth),
               CASE WHEN c.date_of_birth IS NULL THEN NULL
                    WHEN 2016 - YEAR(c.date_of_birth) < 18 THEN N'<18'
                    WHEN 2016 - YEAR(c.date_of_birth) <= 24 THEN N'18-24'
                    WHEN 2016 - YEAR(c.date_of_birth) <= 34 THEN N'25-34'
                    WHEN 2016 - YEAR(c.date_of_birth) <= 44 THEN N'35-44'
                    WHEN 2016 - YEAR(c.date_of_birth) <= 54 THEN N'45-54'
                    ELSE N'55+' END
        FROM dbo.customers c
        LEFT JOIN addr a ON a.customer_id = c.customer_id AND a.rn = 1
        LEFT JOIN loy  l ON l.customer_id = c.customer_id AND l.rn = 1
        LEFT JOIN dw.dim_geography g ON g.country_code = a.country_code;
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),rows_affected=@@ROWCOUNT,status='ok' WHERE run_id=@run_id;
    END TRY BEGIN CATCH
        UPDATE dw.etl_run_log SET finished_at=SYSUTCDATETIME(),status='error',message=ERROR_MESSAGE() WHERE run_id=@run_id; THROW;
    END CATCH
END
GO

-- orchestrator: refresh dimensions in dependency order (geography before customer/shop)
CREATE OR ALTER PROCEDURE batch.usp_refresh_all_dimensions
AS
BEGIN
    SET NOCOUNT ON;
    EXEC batch.usp_refresh_dim_channel;
    EXEC batch.usp_refresh_dim_condition;
    EXEC batch.usp_refresh_dim_currency;
    EXEC batch.usp_refresh_dim_fx;          -- dense as-of FX (facts divide by this)
    EXEC batch.usp_refresh_dim_geography;   -- customer/shop region join
    EXEC batch.usp_refresh_dim_platform;
    EXEC batch.usp_refresh_dim_date;
    EXEC batch.usp_refresh_dim_product;
    EXEC batch.usp_refresh_dim_shop;
    EXEC batch.usp_refresh_dim_staff;
    EXEC batch.usp_refresh_dim_customer;
END
GO
