/* ---- single-entity GETs ---- */
CREATE OR ALTER PROCEDURE rpt.usp_GetCustomer @customer_key BIGINT AS
BEGIN SET NOCOUNT ON;
    SELECT * FROM dw.dim_customer WHERE customer_key = @customer_key;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_GetProduct @product_key BIGINT AS
BEGIN SET NOCOUNT ON;
    SELECT * FROM dw.dim_product WHERE product_key = @product_key;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_GetShop @shop_key BIGINT AS
BEGIN SET NOCOUNT ON;
    SELECT * FROM dw.dim_shop WHERE shop_key = @shop_key;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_GetShopByCode @shop_code NVARCHAR(16) AS
BEGIN SET NOCOUNT ON;
    SELECT * FROM dw.dim_shop WHERE shop_code = @shop_code;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_GetStaff @staff_key BIGINT AS
BEGIN SET NOCOUNT ON;
    SELECT * FROM dw.dim_staff WHERE staff_key = @staff_key;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_GetAccount @account_id BIGINT AS
BEGIN SET NOCOUNT ON;
    SELECT a.*, c.country_code, c.loyalty_tier
    FROM web.accounts a LEFT JOIN dw.dim_customer c ON c.customer_key = a.customer_id
    WHERE a.account_id = @account_id;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_GetAccountByUsername @username NVARCHAR(64) AS
BEGIN SET NOCOUNT ON;
    SELECT * FROM web.accounts WHERE username = @username;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_GetReview @review_id BIGINT AS
BEGIN SET NOCOUNT ON;
    SELECT r.*, a.username FROM web.reviews r JOIN web.accounts a ON a.account_id = r.account_id
    WHERE r.review_id = @review_id;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_GetTransaction @transaction_id BIGINT AS
BEGIN SET NOCOUNT ON;
    SELECT * FROM dbo.transactions WHERE transaction_id = @transaction_id;
    SELECT * FROM dbo.transaction_lines WHERE transaction_id = @transaction_id ORDER BY line_number;
END
GO

/* ---- filtered LISTs ---- */
CREATE OR ALTER PROCEDURE rpt.usp_ListShopsByCountry @country CHAR(2) AS
BEGIN SET NOCOUNT ON;
    SELECT shop_key, shop_code, name, city, opened_date, closed_date, is_open
    FROM dw.dim_shop WHERE country_code = @country ORDER BY name;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_ListProductsByPlatform @platform_name NVARCHAR(100) AS
BEGIN SET NOCOUNT ON;
    SELECT product_key, title, category, publisher, first_release_date
    FROM dw.dim_product WHERE platform_name = @platform_name ORDER BY title;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_ListProductsByPublisher @publisher NVARCHAR(300) AS
BEGIN SET NOCOUNT ON;
    SELECT product_key, title, platform_name, category, first_release_date
    FROM dw.dim_product WHERE publisher = @publisher ORDER BY first_release_date;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_ListProductsByGenre @genre NVARCHAR(120) AS
BEGIN SET NOCOUNT ON;
    SELECT product_key, title, platform_name, publisher
    FROM dw.dim_product WHERE category = @genre AND product_kind='game' ORDER BY title;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_ListCustomersByCohort @signup_year SMALLINT AS
BEGIN SET NOCOUNT ON;
    SELECT customer_key, country_code, city, loyalty_tier, signup_cohort
    FROM dw.dim_customer WHERE signup_year = @signup_year ORDER BY customer_key;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_ListCustomersByCountry @country CHAR(2), @top INT = 1000 AS
BEGIN SET NOCOUNT ON;
    SELECT TOP (@top) customer_key, city, loyalty_tier, signup_date
    FROM dw.dim_customer WHERE country_code = @country ORDER BY signup_date DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_ListReviewsForProduct @product_key BIGINT, @top INT = 100 AS
BEGIN SET NOCOUNT ON;
    SELECT TOP (@top) r.review_id, a.username, r.rating, r.title, r.posted_at, r.helpful_count
    FROM web.reviews r JOIN web.accounts a ON a.account_id = r.account_id
    WHERE r.release_id = @product_key ORDER BY r.helpful_count DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_ListReviewsByAccount @account_id BIGINT AS
BEGIN SET NOCOUNT ON;
    SELECT review_id, release_id, rating, title, posted_at, comment_count, helpful_count
    FROM web.reviews WHERE account_id = @account_id ORDER BY posted_at DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_ListTransactionsForCustomer @customer_key BIGINT, @top INT = 200 AS
BEGIN SET NOCOUNT ON;
    SELECT TOP (@top) occurred_at, transaction_id, product_key, quantity, line_total_usd, is_return
    FROM dw.fact_sales WHERE customer_key = @customer_key ORDER BY occurred_at DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_ListTradeInsForCustomer @customer_key BIGINT AS
BEGIN SET NOCOUNT ON;
    SELECT occurred_at, product_key, offer_amount_usd, is_hardware
    FROM dw.fact_tradein WHERE customer_key = @customer_key ORDER BY occurred_at DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_ListStaffByShop @shop_key BIGINT AS
BEGIN SET NOCOUNT ON;
    SELECT staff_key, full_name, role_title, started_on, ended_on
    FROM dw.dim_staff WHERE home_shop_id = @shop_key ORDER BY started_on;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_SearchCustomersByName @name NVARCHAR(100) AS
BEGIN SET NOCOUNT ON;
    SELECT TOP 500 customer_id, first_name, last_name, signed_up_at
    FROM dbo.customers WHERE last_name = @name ORDER BY signed_up_at;   -- SARGable equality
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_SearchProductsByTitlePrefix @prefix NVARCHAR(100) AS
BEGIN SET NOCOUNT ON;
    SELECT product_key, title, platform_name, publisher
    FROM dw.dim_product WHERE title LIKE @prefix + '%'   -- trailing-only wildcard = index-friendly
    ORDER BY title;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_ListRecentReviews @top INT = 100 AS
BEGIN SET NOCOUNT ON;
    SELECT TOP (@top) review_id, account_id, release_id, rating, posted_at
    FROM web.reviews ORDER BY posted_at DESC;
END
GO
