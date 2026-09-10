/* =============================================================
   batch — maintenance / DBA ops (PostgreSQL port)
   -------------------------------------------------------------
   The "run at 2am" housekeeping + health reports. The SQL Server DMV
   procs (sys.dm_db_*) have no columnstore/fragmentation analogue on
   Postgres, so they are rewritten to the equivalent pg_stat_* /
   pg_catalog views — a "same intent, different mechanism" comparison.
   Actions (stats refresh, log purge) are PROCEDUREs; the health
   reports are refcursor FUNCTIONs (like the rpt library).
   ============================================================= */

/* ---- refresh statistics on the dw tables (post-load hygiene) = ANALYZE.
   Dynamic UPDATE STATISTICS over sys.tables -> EXECUTE format('ANALYZE ...'). */
CREATE OR REPLACE PROCEDURE batch.usp_UpdateStatsAll()
LANGUAGE plpgsql AS $$
DECLARE r record;
BEGIN
    FOR r IN SELECT tablename FROM pg_tables WHERE schemaname='dw' LOOP
        EXECUTE format('ANALYZE dw.%I', r.tablename);
    END LOOP;
    RAISE NOTICE 'ANALYZE complete for schema dw';
END $$;

/* ---- "index fragmentation": Postgres btrees don't fragment like SQL Server,
   so the DMV equivalent is index size + scan activity (pg_stat_user_indexes /
   pg_class). p_min_pages filters by 8 KB page count, as the original did. ---- */
CREATE OR REPLACE FUNCTION batch.usp_IndexFragmentationReport(p_min_pages int DEFAULT 1000)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT t.relname AS table_name, i.relname AS index_name,
           am.amname AS index_type,
           (pg_relation_size(i.oid)/8192)::bigint AS page_count,
           COALESCE(s.idx_scan,0) AS index_scans
    FROM pg_class i
    JOIN pg_index ix ON ix.indexrelid = i.oid
    JOIN pg_class t ON t.oid = ix.indrelid
    JOIN pg_namespace n ON n.oid = t.relnamespace
    JOIN pg_am am ON am.oid = i.relam
    LEFT JOIN pg_stat_user_indexes s ON s.indexrelid = i.oid
    WHERE n.nspname='dw' AND (pg_relation_size(i.oid)/8192) >= p_min_pages
    ORDER BY page_count DESC;
    RETURN c;
END $$;

/* ---- "columnstore health": no columnstore on Postgres; the nearest storage
   view is heap live/dead tuples + size (dead tuples ~ the rowgroup
   "deleted_rows" a VACUUM would reclaim). ---- */
CREATE OR REPLACE FUNCTION batch.usp_ColumnstoreHealth()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT relname AS table_name,
           n_live_tup AS total_rows, n_dead_tup AS deleted_rows,
           CAST(pg_total_relation_size(relid)/1024.0/1024 AS decimal(12,1)) AS size_mb
    FROM pg_stat_user_tables
    WHERE schemaname='dw'
    ORDER BY size_mb DESC;
    RETURN c;
END $$;

/* ---- dw table sizes (pg_stat_user_tables + pg_total_relation_size) ---- */
CREATE OR REPLACE FUNCTION batch.usp_TableSizes()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT schemaname AS sch, relname AS table_name,
           n_live_tup AS rows_,
           CAST(pg_total_relation_size(relid)/1024.0/1024 AS decimal(14,1)) AS reserved_mb
    FROM pg_stat_user_tables
    WHERE schemaname='dw'
    ORDER BY reserved_mb DESC;
    RETURN c;
END $$;

/* ---- recent ETL run history (from the run log) ---- */
CREATE OR REPLACE FUNCTION batch.usp_EtlRunHistory(p_top int DEFAULT 40)
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT run_id, proc_name, target_object, mode, status,
           rows_affected, extract(epoch from (finished_at - started_at))::int AS secs, started_at
    FROM dw.etl_run_log ORDER BY run_id DESC
    LIMIT p_top;
    RETURN c;
END $$;

/* ---- housekeeping: trim the ETL log (writes dw only) ---- */
CREATE OR REPLACE PROCEDURE batch.usp_PurgeEtlLog(p_keep_days int DEFAULT 90)
LANGUAGE plpgsql AS $$
DECLARE v_n bigint;
BEGIN
    DELETE FROM dw.etl_run_log WHERE started_at < (now() AT TIME ZONE 'UTC') - (p_keep_days || ' days')::interval;
    GET DIAGNOSTICS v_n = ROW_COUNT;
    RAISE NOTICE 'purged % etl_run_log rows', v_n;
END $$;

/* ---- data freshness: latest fact dates vs OLTP source ---- */
CREATE OR REPLACE FUNCTION batch.usp_CheckDataFreshness()
RETURNS refcursor LANGUAGE plpgsql AS $$
DECLARE c refcursor;
BEGIN
    OPEN c FOR
    SELECT 'fact_sales' AS fact, (SELECT MAX(occurred_at) FROM dw.fact_sales) AS dw_max,
           (SELECT MAX(occurred_at) FROM public.transactions) AS oltp_max
    UNION ALL
    SELECT 'fact_web_activity', (SELECT MAX(occurred_at) FROM dw.fact_web_activity),
           (SELECT MAX(occurred_at) FROM web.page_views)
    UNION ALL
    SELECT 'fact_tradein', (SELECT MAX(occurred_at) FROM dw.fact_tradein),
           (SELECT MAX(occurred_at) FROM public.trade_ins);
    RETURN c;
END $$;
