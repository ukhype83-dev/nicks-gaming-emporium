/* =============================================================
   loadgen — part 2: stress orchestration + workload mixes (PG port)
   Drives the batch heavy jobs + heavy reports from one entry point.
   ============================================================= */

/* ---- fire a random long-runner (for monitoring-tool exercising) ---- */
CREATE OR REPLACE PROCEDURE loadgen.usp_StressTest(p_seconds int DEFAULT 30)
LANGUAGE plpgsql AS $$
DECLARE v_which int := 1 + floor(random()*6)::int;
BEGIN
    IF    v_which = 1 THEN CALL batch.usp_ImportSupplierPriceFeed(p_seconds);
    ELSIF v_which = 2 THEN CALL batch.usp_RecalculateLoyaltyPoints(p_seconds);
    ELSIF v_which = 3 THEN PERFORM batch.drain(rpt.usp_rpt_AnnualSalesAudit(2));
    ELSIF v_which = 4 THEN PERFORM batch.drain(rpt.usp_rpt_FullLedgerExport(1000000));
    ELSIF v_which = 5 THEN CALL batch.usp_RecalculateCustomerTiers(2000);
    ELSE                   CALL batch.usp_MonthEndClose(p_seconds);
    END IF;
END $$;

/* ---- a burst of clean reports back to back (read storm) ---- */
CREATE OR REPLACE PROCEDURE loadgen.usp_ReportStorm(p_iterations int DEFAULT 25)
LANGUAGE plpgsql AS $$
DECLARE v_i int := 0;
BEGIN
    WHILE v_i < p_iterations LOOP
        CALL loadgen.usp_RandomReport(0);   -- clean reports only
        v_i := v_i + 1;
    END LOOP;
END $$;

/* ---- simulate the overnight batch (write load on the reporting layer) ---- */
CREATE OR REPLACE PROCEDURE loadgen.usp_OvernightBatchSim()
LANGUAGE plpgsql AS $$
BEGIN
    CALL batch.usp_refresh_all_rollups();          -- re-aggregate (idempotent)
    CALL batch.usp_UpdateStatsAll();               -- stats hygiene (ANALYZE)
    PERFORM batch.drain(batch.usp_ColumnstoreHealth());   -- health snapshot
END $$;

/* ---- a mixed day: reports + the occasional server-melter ---- */
CREATE OR REPLACE PROCEDURE loadgen.usp_MixedTradingDay(p_iterations int DEFAULT 20, p_chaos int DEFAULT 30)
LANGUAGE plpgsql AS $$
DECLARE v_i int := 0;
BEGIN
    WHILE v_i < p_iterations LOOP
        CALL loadgen.usp_RandomReport(p_chaos);
        IF (floor(random()*10)::int) = 0 THEN PERFORM batch.drain(rpt.usp_rpt_AnnualSalesAudit(1)); END IF;
        v_i := v_i + 1;
    END LOOP;
END $$;

/* ---- named preset for a monitoring soak test ---- */
CREATE OR REPLACE PROCEDURE loadgen.usp_MonitoringSoak(p_rounds int DEFAULT 5, p_seconds int DEFAULT 20)
LANGUAGE plpgsql AS $$
DECLARE v_i int := 0;
BEGIN
    WHILE v_i < p_rounds LOOP
        CALL loadgen.usp_StressTest(p_seconds);
        v_i := v_i + 1;
    END LOOP;
END $$;
