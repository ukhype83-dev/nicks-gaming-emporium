/* =============================================================
   rpt — entity lookups & lists (PostgreSQL port)
   Single-entity gets + filtered lists, as refcursor functions.
   See rpt_sales.sql for the refcursor consumption pattern.
   ============================================================= */

/* ---- single-entity GETs ---- */
CREATE OR REPLACE FUNCTION rpt.usp_GetCustomer(p_customer_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; BEGIN OPEN c FOR SELECT * FROM dw.dim_customer WHERE customer_key = p_customer_key; RETURN c; END $$;

CREATE OR REPLACE FUNCTION rpt.usp_GetProduct(p_product_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; BEGIN OPEN c FOR SELECT * FROM dw.dim_product WHERE product_key = p_product_key; RETURN c; END $$;

CREATE OR REPLACE FUNCTION rpt.usp_GetShop(p_shop_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; BEGIN OPEN c FOR SELECT * FROM dw.dim_shop WHERE shop_key = p_shop_key; RETURN c; END $$;

CREATE OR REPLACE FUNCTION rpt.usp_GetShopByCode(p_shop_code varchar(16))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; BEGIN OPEN c FOR SELECT * FROM dw.dim_shop WHERE shop_code = p_shop_code; RETURN c; END $$;

CREATE OR REPLACE FUNCTION rpt.usp_GetStaff(p_staff_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; BEGIN OPEN c FOR SELECT * FROM dw.dim_staff WHERE staff_key = p_staff_key; RETURN c; END $$;

CREATE OR REPLACE FUNCTION rpt.usp_GetAccount(p_account_id bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT a.*, c.country_code, c.loyalty_tier
    FROM web.accounts a LEFT JOIN dw.dim_customer c ON c.customer_key = a.customer_id
    WHERE a.account_id = p_account_id;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_GetAccountByUsername(p_username varchar(64))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor; BEGIN OPEN c FOR SELECT * FROM web.accounts WHERE username = p_username; RETURN c; END $$;

CREATE OR REPLACE FUNCTION rpt.usp_GetReview(p_review_id bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT r.*, a.username FROM web.reviews r JOIN web.accounts a ON a.account_id = r.account_id
    WHERE r.review_id = p_review_id;
    RETURN c;
END $$;

-- Two result sets (header + lines) -> two refcursors via SETOF refcursor.
CREATE OR REPLACE FUNCTION rpt.usp_GetTransaction(p_transaction_id bigint)
RETURNS SETOF refcursor LANGUAGE plpgsql AS $$
DECLARE c1 refcursor; c2 refcursor;
BEGIN
    OPEN c1 FOR SELECT * FROM public.transactions WHERE transaction_id = p_transaction_id;
    RETURN NEXT c1;
    OPEN c2 FOR SELECT * FROM public.transaction_lines WHERE transaction_id = p_transaction_id ORDER BY line_number;
    RETURN NEXT c2;
END $$;

/* ---- filtered LISTs ---- */
CREATE OR REPLACE FUNCTION rpt.usp_ListShopsByCountry(p_country char(2))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT shop_key, shop_code, name, city, opened_date, closed_date, is_open
    FROM dw.dim_shop WHERE country_code = p_country ORDER BY name;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_ListProductsByPlatform(p_platform_name varchar(100))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT product_key, title, category, publisher, first_release_date
    FROM dw.dim_product WHERE platform_name = p_platform_name ORDER BY title;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_ListProductsByPublisher(p_publisher varchar(300))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT product_key, title, platform_name, category, first_release_date
    FROM dw.dim_product WHERE publisher = p_publisher ORDER BY first_release_date;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_ListProductsByGenre(p_genre varchar(120))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT product_key, title, platform_name, publisher
    FROM dw.dim_product WHERE category = p_genre AND product_kind='game' ORDER BY title;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_ListCustomersByCohort(p_signup_year int)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT customer_key, country_code, city, loyalty_tier, signup_cohort
    FROM dw.dim_customer WHERE signup_year = p_signup_year ORDER BY customer_key;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_ListCustomersByCountry(p_country char(2), p_top int DEFAULT 1000)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT customer_key, city, loyalty_tier, signup_date
    FROM dw.dim_customer WHERE country_code = p_country ORDER BY signup_date DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_ListReviewsForProduct(p_product_key bigint, p_top int DEFAULT 100)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT r.review_id, a.username, r.rating, r.title, r.posted_at, r.helpful_count
    FROM web.reviews r JOIN web.accounts a ON a.account_id = r.account_id
    WHERE r.release_id = p_product_key ORDER BY r.helpful_count DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_ListReviewsByAccount(p_account_id bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT review_id, release_id, rating, title, posted_at, comment_count, helpful_count
    FROM web.reviews WHERE account_id = p_account_id ORDER BY posted_at DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_ListTransactionsForCustomer(p_customer_key bigint, p_top int DEFAULT 200)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT occurred_at, transaction_id, product_key, quantity, line_total_usd, is_return
    FROM dw.fact_sales WHERE customer_key = p_customer_key ORDER BY occurred_at DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_ListTradeInsForCustomer(p_customer_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT occurred_at, product_key, offer_amount_usd, is_hardware
    FROM dw.fact_tradein WHERE customer_key = p_customer_key ORDER BY occurred_at DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_ListStaffByShop(p_shop_key bigint)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT staff_key, full_name, role_title, started_on, ended_on
    FROM dw.dim_staff WHERE home_shop_id = p_shop_key ORDER BY started_on;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_SearchCustomersByName(p_name varchar(100))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT customer_id, first_name, last_name, signed_up_at
    FROM public.customers WHERE last_name = p_name ORDER BY signed_up_at   -- SARGable equality
    LIMIT 500;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_SearchProductsByTitlePrefix(p_prefix varchar(100))
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT product_key, title, platform_name, publisher
    FROM dw.dim_product WHERE title LIKE p_prefix || '%'   -- trailing-only wildcard = index-friendly
    ORDER BY title;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_ListRecentReviews(p_top int DEFAULT 100)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT review_id, account_id, release_id, rating, posted_at
    FROM web.reviews ORDER BY posted_at DESC
    LIMIT p_top;
    RETURN c;
END $$;
