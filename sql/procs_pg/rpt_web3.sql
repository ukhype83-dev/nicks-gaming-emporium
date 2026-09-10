/* =============================================================
   rpt — community / web analytics, part 3 (PostgreSQL port)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ReviewsByYear()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT extract(year from posted_at) AS yr, COUNT(*) AS reviews,
           CAST(AVG(rating::float8) AS decimal(4,2)) AS avg_rating,
           SUM(comment_count) AS comments, SUM(helpful_count) AS helpful_votes
    FROM web.reviews GROUP BY extract(year from posted_at) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_TrafficByYear()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT LEFT(date_key::text,4) AS yr,
           SUM(page_views) AS page_views, SUM(sessions) AS sessions,
           SUM(logged_in_views) AS logged_in_views
    FROM dw.agg_web_traffic_daily GROUP BY LEFT(date_key::text,4) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ReferrerBreakdown(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    -- referrer_domain lives on web.page_views (not carried into the fact)
    SELECT COALESCE(referrer_domain, '(direct)') AS referrer, COUNT(*) AS views
    FROM web.page_views
    WHERE (p_year IS NULL OR extract(year from occurred_at)=p_year)
    GROUP BY referrer_domain ORDER BY views DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_UserAgentBreakdown(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT user_agent_family, is_bot, COUNT(*) AS views
    FROM dw.fact_web_activity
    WHERE (p_year IS NULL OR extract(year from occurred_at)=p_year)
    GROUP BY user_agent_family, is_bot ORDER BY views DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_HttpStatusMix(p_year int DEFAULT NULL)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT http_status, COUNT(*) AS views
    FROM dw.fact_web_activity
    WHERE (p_year IS NULL OR extract(year from occurred_at)=p_year)
    GROUP BY http_status ORDER BY views DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_AccountsByYear()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT extract(year from created_at) AS yr, COUNT(*) AS accounts_created
    FROM web.accounts GROUP BY extract(year from created_at) ORDER BY yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_MostReviewedPlatforms(p_top int DEFAULT 15)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT p.platform_name, COUNT(*) AS reviews,
           CAST(AVG(r.rating::float8) AS decimal(4,2)) AS avg_rating
    FROM web.reviews r JOIN dw.dim_product p ON p.product_key = r.release_id
    WHERE r.release_id IS NOT NULL
    GROUP BY p.platform_name ORDER BY reviews DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_CommentDepthDistribution()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT CASE WHEN comment_count=0 THEN '0' WHEN comment_count<=2 THEN '1-2'
                WHEN comment_count<=10 THEN '3-10' WHEN comment_count<=40 THEN '11-40'
                ELSE '40+' END AS comment_band,
           COUNT(*) AS reviews
    FROM web.reviews
    GROUP BY CASE WHEN comment_count=0 THEN '0' WHEN comment_count<=2 THEN '1-2'
                  WHEN comment_count<=10 THEN '3-10' WHEN comment_count<=40 THEN '11-40'
                  ELSE '40+' END
    ORDER BY MIN(comment_count);
    RETURN c;
END $$;
