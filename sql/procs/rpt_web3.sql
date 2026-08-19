CREATE OR ALTER PROCEDURE rpt.usp_rpt_ReviewsByYear AS
BEGIN SET NOCOUNT ON;
    SELECT YEAR(posted_at) AS yr, COUNT_BIG(*) AS reviews,
           CAST(AVG(CAST(rating AS FLOAT)) AS DECIMAL(4,2)) AS avg_rating,
           SUM(comment_count) AS comments, SUM(helpful_count) AS helpful_votes
    FROM web.reviews GROUP BY YEAR(posted_at) ORDER BY yr;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_TrafficByYear AS
BEGIN SET NOCOUNT ON;
    SELECT LEFT(CAST(date_key AS CHAR(8)),4) AS yr,
           SUM(page_views) AS page_views, SUM(sessions) AS sessions,
           SUM(logged_in_views) AS logged_in_views
    FROM dw.agg_web_traffic_daily GROUP BY LEFT(CAST(date_key AS CHAR(8)),4) ORDER BY yr;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_ReferrerBreakdown @year SMALLINT=NULL AS
BEGIN SET NOCOUNT ON;
    -- referrer_domain lives on web.page_views (not carried into the fact)
    SELECT ISNULL(referrer_domain, N'(direct)') AS referrer, COUNT_BIG(*) AS views
    FROM web.page_views
    WHERE (@year IS NULL OR YEAR(occurred_at)=@year)
    GROUP BY referrer_domain ORDER BY views DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_UserAgentBreakdown @year SMALLINT=NULL AS
BEGIN SET NOCOUNT ON;
    SELECT user_agent_family, is_bot, COUNT_BIG(*) AS views
    FROM dw.fact_web_activity
    WHERE (@year IS NULL OR YEAR(occurred_at)=@year)
    GROUP BY user_agent_family, is_bot ORDER BY views DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_HttpStatusMix @year SMALLINT=NULL AS
BEGIN SET NOCOUNT ON;
    SELECT http_status, COUNT_BIG(*) AS views
    FROM dw.fact_web_activity
    WHERE (@year IS NULL OR YEAR(occurred_at)=@year)
    GROUP BY http_status ORDER BY views DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_AccountsByYear AS
BEGIN SET NOCOUNT ON;
    SELECT YEAR(created_at) AS yr, COUNT_BIG(*) AS accounts_created
    FROM web.accounts GROUP BY YEAR(created_at) ORDER BY yr;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_MostReviewedPlatforms @top INT=15 AS
BEGIN SET NOCOUNT ON;
    SELECT TOP (@top) p.platform_name, COUNT_BIG(*) AS reviews,
           CAST(AVG(CAST(r.rating AS FLOAT)) AS DECIMAL(4,2)) AS avg_rating
    FROM web.reviews r JOIN dw.dim_product p ON p.product_key = r.release_id
    WHERE r.release_id IS NOT NULL
    GROUP BY p.platform_name ORDER BY reviews DESC;
END
GO
CREATE OR ALTER PROCEDURE rpt.usp_rpt_CommentDepthDistribution AS
BEGIN SET NOCOUNT ON;
    SELECT CASE WHEN comment_count=0 THEN N'0' WHEN comment_count<=2 THEN N'1-2'
                WHEN comment_count<=10 THEN N'3-10' WHEN comment_count<=40 THEN N'11-40'
                ELSE N'40+' END AS comment_band,
           COUNT_BIG(*) AS reviews
    FROM web.reviews
    GROUP BY CASE WHEN comment_count=0 THEN N'0' WHEN comment_count<=2 THEN N'1-2'
                  WHEN comment_count<=10 THEN N'3-10' WHEN comment_count<=40 THEN N'11-40'
                  ELSE N'40+' END
    ORDER BY MIN(comment_count);
END
GO
