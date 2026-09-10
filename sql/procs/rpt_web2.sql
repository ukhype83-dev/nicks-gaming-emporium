CREATE OR ALTER PROCEDURE rpt.usp_rpt_CommentActivity
AS
BEGIN
    SET NOCOUNT ON;
    SELECT CONVERT(CHAR(7), posted_at, 126) AS year_month,
           COUNT_BIG(*) AS comments,
           COUNT(DISTINCT review_id) AS reviews_commented,
           COUNT(DISTINCT account_id) AS distinct_commenters
    FROM web.review_comments
    GROUP BY CONVERT(CHAR(7), posted_at, 126) ORDER BY year_month;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_VotePatterns
AS
BEGIN
    SET NOCOUNT ON;
    SELECT vote_type, COUNT_BIG(*) AS votes,
           CAST(100.0*COUNT_BIG(*)/SUM(COUNT_BIG(*)) OVER () AS DECIMAL(5,2)) AS pct
    FROM web.review_votes GROUP BY vote_type ORDER BY votes DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_ReviewLengthDistribution
AS
BEGIN
    SET NOCOUNT ON;
    SELECT CASE WHEN LEN(body)=0 THEN N'rating-only' WHEN LEN(body)<100 THEN N'short'
                WHEN LEN(body)<400 THEN N'medium' ELSE N'long' END AS length_band,
           COUNT_BIG(*) AS reviews,
           CAST(AVG(CAST(rating AS FLOAT)) AS DECIMAL(4,2)) AS avg_rating
    FROM web.reviews
    GROUP BY CASE WHEN LEN(body)=0 THEN N'rating-only' WHEN LEN(body)<100 THEN N'short'
                  WHEN LEN(body)<400 THEN N'medium' ELSE N'long' END
    ORDER BY reviews DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_LanguageXRating
AS
BEGIN
    SET NOCOUNT ON;
    SELECT language_code,
           COUNT_BIG(*) AS reviews,
           CAST(AVG(CAST(rating AS FLOAT)) AS DECIMAL(4,2)) AS avg_rating,
           SUM(CASE WHEN rating>=4 THEN 1 ELSE 0 END) AS positive,
           SUM(CASE WHEN rating<=2 THEN 1 ELSE 0 END) AS negative
    FROM web.reviews GROUP BY language_code ORDER BY reviews DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_HotThreads
    @top INT = 25
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) r.review_id, p.title, a.username AS author,
           r.rating, r.comment_count, r.helpful_count, r.funny_count
    FROM web.reviews r
    JOIN web.accounts a ON a.account_id = r.account_id
    LEFT JOIN dw.dim_product p ON p.product_key = r.release_id
    ORDER BY r.comment_count DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_EngagementByYear
AS
BEGIN
    SET NOCOUNT ON;
    SELECT y.yr,
           (SELECT COUNT_BIG(*) FROM web.reviews r WHERE YEAR(r.posted_at)=y.yr) AS reviews,
           (SELECT COUNT_BIG(*) FROM web.review_comments c WHERE YEAR(c.posted_at)=y.yr) AS comments,
           (SELECT COUNT_BIG(*) FROM web.review_votes v WHERE YEAR(v.occurred_at)=y.yr) AS votes
    FROM (SELECT DISTINCT YEAR(posted_at) AS yr FROM web.reviews) y
    ORDER BY y.yr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_VerifiedVsUnverified
AS
BEGIN
    SET NOCOUNT ON;
    SELECT CASE WHEN is_verified_purchase=1 THEN N'verified' ELSE N'unverified' END AS kind,
           COUNT_BIG(*) AS reviews,
           CAST(AVG(CAST(rating AS FLOAT)) AS DECIMAL(4,2)) AS avg_rating,
           CAST(AVG(CAST(helpful_count AS FLOAT)) AS DECIMAL(6,2)) AS avg_helpful
    FROM web.reviews
    GROUP BY is_verified_purchase;
END
GO
