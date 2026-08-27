/* =============================================================
   batch — shared ETL-log helper (PostgreSQL)
   -------------------------------------------------------------
   Replaces the T-SQL "INSERT dw.etl_run_log ... ; SET @run_id =
   SCOPE_IDENTITY()" preamble that opened every refresh proc.
   log_start inserts a 'running' row and returns its run_id via
   RETURNING; each proc inlines the success UPDATE (status='ok',
   rows_affected) and — for the dim/rollup procs — a best-effort
   error UPDATE in an EXCEPTION block.

   PORT NOTE (a genuine SQL-Server vs PostgreSQL divergence, worth a
   lab): a PL/pgSQL block with an EXCEPTION handler runs in an
   implicit subtransaction, so when a dim/rollup proc raises, the
   block is rolled back to its savepoint — including this log_start
   row — before the handler runs; the error UPDATE therefore hits
   zero rows (best-effort) and the RAISE re-surfaces the failure.
   T-SQL's TRY/CATCH keeps the row and records status='error'. The
   fact/wide procs COMMIT per month and so carry NO exception handler
   at all (COMMIT and EXCEPTION are mutually exclusive in one block);
   they fail loudly by propagation instead.
   ============================================================= */
CREATE OR REPLACE FUNCTION batch.log_start(p_proc text, p_target text, p_mode text)
RETURNS bigint LANGUAGE sql AS $$
    INSERT INTO dw.etl_run_log(proc_name, target_object, mode)
    VALUES (p_proc, p_target, p_mode)
    RETURNING run_id;
$$;
