/* =============================================================
   rpt — web / community reports (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ReviewSentimentTrend()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT year_month, review_count, avg_rating,
           rating_only_count, verified_count,
           CAST(100.0*rating_only_count/NULLIF(review_count,0) AS decimal(5,1)) AS rating_only_pct
    FROM dw.agg_review_sentiment_by_month ORDER BY year_month;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TrafficByDay(p_from_key int DEFAULT 20040101, p_to_key int DEFAULT 20161231)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT a.date_key, d.full_date, a.page_views, a.sessions,
           a.bot_views, a.human_views, a.logged_in_views, a.distinct_countries
    FROM dw.agg_web_traffic_daily a
    JOIN dw.dim_date d ON d.date_key = a.date_key
    WHERE a.date_key BETWEEN p_from_key AND p_to_key
    ORDER BY a.date_key;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_BotVsHuman(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT LEFT(a.date_key::text,4) AS yr,
           SUM(a.page_views) AS page_views,
           SUM(a.bot_views)  AS bot_views,
           SUM(a.human_views) AS human_views,
           CAST(100.0*SUM(a.bot_views)/NULLIF(SUM(a.page_views),0) AS decimal(5,1)) AS bot_pct
    FROM dw.agg_web_traffic_daily a
    WHERE (p_year IS NULL OR LEFT(a.date_key::text,4) = p_year::text)
    GROUP BY LEFT(a.date_key::text,4) ORDER BY yr;
    RETURN c;
END $$;

/* The September-2016 death-rattle: traffic spike + crawler takeover. */
CREATE OR REPLACE FUNCTION rpt.usp_rpt_DeathRattle()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT LEFT(date_key::text,6) AS year_month,
           SUM(page_views) AS page_views,
           SUM(page_views)/NULLIF(COUNT(*),0) AS pv_per_day,
           CAST(100.0*SUM(bot_views)/NULLIF(SUM(page_views),0) AS decimal(5,1)) AS bot_pct
    FROM dw.agg_web_traffic_daily
    WHERE date_key >= 20160101
    GROUP BY LEFT(date_key::text,6) ORDER BY year_month;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TopReviewers(p_top int DEFAULT 25)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT a.account_id, a.username,
           COUNT(*) AS reviews,
           CAST(AVG(r.rating::float8) AS decimal(4,2)) AS avg_rating,
           SUM(r.helpful_count) AS helpful_votes
    FROM web.reviews r
    JOIN web.accounts a ON a.account_id = r.account_id
    GROUP BY a.account_id, a.username
    ORDER BY reviews DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_MostReviewedProducts(p_top int DEFAULT 25)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.product_key, p.title, p.platform_name,
           COUNT(*) AS reviews,
           CAST(AVG(r.rating::float8) AS decimal(4,2)) AS avg_rating
    FROM web.reviews r
    JOIN dw.dim_product p ON p.product_key = r.release_id     -- game reviews (release_id = product_key)
    WHERE r.release_id IS NOT NULL
    GROUP BY p.product_key, p.title, p.platform_name
    ORDER BY reviews DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ReviewsByLanguage()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT language_code, COUNT(*) AS reviews,
           CAST(100.0*COUNT(*)/SUM(COUNT(*)) OVER () AS decimal(5,2)) AS pct,
           CAST(AVG(rating::float8) AS decimal(4,2)) AS avg_rating
    FROM web.reviews GROUP BY language_code ORDER BY reviews DESC;
    RETURN c;
END $$;
