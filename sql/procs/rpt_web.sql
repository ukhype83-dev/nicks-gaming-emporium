CREATE OR ALTER PROCEDURE rpt.usp_rpt_ReviewSentimentTrend
AS
BEGIN
    SET NOCOUNT ON;
    SELECT year_month, review_count, avg_rating,
           rating_only_count, verified_count,
           CAST(100.0*rating_only_count/NULLIF(review_count,0) AS DECIMAL(5,1)) AS rating_only_pct
    FROM dw.agg_review_sentiment_by_month ORDER BY year_month;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_TrafficByDay
    @from_key INT = 20040101, @to_key INT = 20161231
AS
BEGIN
    SET NOCOUNT ON;
    SELECT a.date_key, d.full_date, a.page_views, a.sessions,
           a.bot_views, a.human_views, a.logged_in_views, a.distinct_countries
    FROM dw.agg_web_traffic_daily a
    JOIN dw.dim_date d ON d.date_key = a.date_key
    WHERE a.date_key BETWEEN @from_key AND @to_key
    ORDER BY a.date_key;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_BotVsHuman
    @year SMALLINT = NULL
AS
BEGIN
    SET NOCOUNT ON;
    SELECT LEFT(CAST(a.date_key AS CHAR(8)),4) AS yr,
           SUM(a.page_views) AS page_views,
           SUM(a.bot_views)  AS bot_views,
           SUM(a.human_views) AS human_views,
           CAST(100.0*SUM(a.bot_views)/NULLIF(SUM(a.page_views),0) AS DECIMAL(5,1)) AS bot_pct
    FROM dw.agg_web_traffic_daily a
    WHERE (@year IS NULL OR LEFT(CAST(a.date_key AS CHAR(8)),4) = CAST(@year AS CHAR(4)))
    GROUP BY LEFT(CAST(a.date_key AS CHAR(8)),4) ORDER BY yr;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_DeathRattle
AS
BEGIN
    SET NOCOUNT ON;
    SELECT LEFT(CAST(date_key AS CHAR(8)),6) AS year_month,
           SUM(page_views) AS page_views,
           SUM(page_views)/NULLIF(COUNT(*),0) AS pv_per_day,
           CAST(100.0*SUM(bot_views)/NULLIF(SUM(page_views),0) AS DECIMAL(5,1)) AS bot_pct
    FROM dw.agg_web_traffic_daily
    WHERE date_key >= 20160101
    GROUP BY LEFT(CAST(date_key AS CHAR(8)),6) ORDER BY year_month;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_TopReviewers
    @top INT = 25
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) a.account_id, a.username,
           COUNT_BIG(*) AS reviews,
           CAST(AVG(CAST(r.rating AS FLOAT)) AS DECIMAL(4,2)) AS avg_rating,
           SUM(r.helpful_count) AS helpful_votes
    FROM web.reviews r
    JOIN web.accounts a ON a.account_id = r.account_id
    GROUP BY a.account_id, a.username
    ORDER BY reviews DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_MostReviewedProducts
    @top INT = 25
AS
BEGIN
    SET NOCOUNT ON;
    SELECT TOP (@top) p.product_key, p.title, p.platform_name,
           COUNT_BIG(*) AS reviews,
           CAST(AVG(CAST(r.rating AS FLOAT)) AS DECIMAL(4,2)) AS avg_rating
    FROM web.reviews r
    JOIN dw.dim_product p ON p.product_key = r.release_id     -- game reviews (release_id = product_key)
    WHERE r.release_id IS NOT NULL
    GROUP BY p.product_key, p.title, p.platform_name
    ORDER BY reviews DESC;
END
GO

CREATE OR ALTER PROCEDURE rpt.usp_rpt_ReviewsByLanguage
AS
BEGIN
    SET NOCOUNT ON;
    SELECT language_code, COUNT_BIG(*) AS reviews,
           CAST(100.0*COUNT_BIG(*)/SUM(COUNT_BIG(*)) OVER () AS DECIMAL(5,2)) AS pct,
           CAST(AVG(CAST(rating AS FLOAT)) AS DECIMAL(4,2)) AS avg_rating
    FROM web.reviews GROUP BY language_code ORDER BY reviews DESC;
END
GO
