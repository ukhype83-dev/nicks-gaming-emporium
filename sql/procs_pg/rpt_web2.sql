/* =============================================================
   rpt — community analytics, part 2 (PostgreSQL port over web.*)
   ============================================================= */

CREATE OR REPLACE FUNCTION rpt.usp_rpt_CommentActivity()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT to_char(posted_at,'YYYY-MM') AS year_month,
           COUNT(*) AS comments,
           COUNT(DISTINCT review_id) AS reviews_commented,
           COUNT(DISTINCT account_id) AS distinct_commenters
    FROM web.review_comments
    GROUP BY to_char(posted_at,'YYYY-MM') ORDER BY year_month;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_VotePatterns()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT vote_type, COUNT(*) AS votes,
           CAST(100.0*COUNT(*)/SUM(COUNT(*)) OVER () AS decimal(5,2)) AS pct
    FROM web.review_votes GROUP BY vote_type ORDER BY votes DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_ReviewLengthDistribution()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT CASE WHEN length(body)=0 THEN 'rating-only' WHEN length(body)<100 THEN 'short'
                WHEN length(body)<400 THEN 'medium' ELSE 'long' END AS length_band,
           COUNT(*) AS reviews,
           CAST(AVG(rating::float8) AS decimal(4,2)) AS avg_rating
    FROM web.reviews
    GROUP BY CASE WHEN length(body)=0 THEN 'rating-only' WHEN length(body)<100 THEN 'short'
                  WHEN length(body)<400 THEN 'medium' ELSE 'long' END
    ORDER BY reviews DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_LanguageXRating()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT language_code,
           COUNT(*) AS reviews,
           CAST(AVG(rating::float8) AS decimal(4,2)) AS avg_rating,
           SUM(CASE WHEN rating>=4 THEN 1 ELSE 0 END) AS positive,
           SUM(CASE WHEN rating<=2 THEN 1 ELSE 0 END) AS negative
    FROM web.reviews GROUP BY language_code ORDER BY reviews DESC;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_HotThreads(p_top int DEFAULT 25)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT r.review_id, p.title, a.username AS author,
           r.rating, r.comment_count, r.helpful_count, r.funny_count
    FROM web.reviews r
    JOIN web.accounts a ON a.account_id = r.account_id
    LEFT JOIN dw.dim_product p ON p.product_key = r.release_id
    ORDER BY r.comment_count DESC
    LIMIT p_top;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_EngagementByYear()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT y.yr,
           (SELECT COUNT(*) FROM web.reviews r WHERE extract(year from r.posted_at)=y.yr) AS reviews,
           (SELECT COUNT(*) FROM web.review_comments cc WHERE extract(year from cc.posted_at)=y.yr) AS comments,
           (SELECT COUNT(*) FROM web.review_votes v WHERE extract(year from v.occurred_at)=y.yr) AS votes
    FROM (SELECT DISTINCT extract(year from posted_at) AS yr FROM web.reviews) y
    ORDER BY y.yr;
    RETURN c;
END $$;

CREATE OR REPLACE FUNCTION rpt.usp_rpt_VerifiedVsUnverified()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT CASE WHEN is_verified_purchase THEN 'verified' ELSE 'unverified' END AS kind,
           COUNT(*) AS reviews,
           CAST(AVG(rating::float8) AS decimal(4,2)) AS avg_rating,
           CAST(AVG(helpful_count::float8) AS decimal(6,2)) AS avg_helpful
    FROM web.reviews
    GROUP BY is_verified_purchase;
    RETURN c;
END $$;
