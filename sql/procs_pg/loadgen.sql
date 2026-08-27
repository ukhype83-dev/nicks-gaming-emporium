/* =============================================================
   loadgen — workload orchestration (PostgreSQL port)
   -------------------------------------------------------------
   Concurrent sessions CALL these to drive read load (rpt.* reports,
   drained to actually execute) and write load (batch.* refreshes).
   NEWID()/CHECKSUM(NEWID()) -> random(); EXEC -> CALL (procedures)
   or PERFORM batch.drain(rpt.usp_*(...)) (report functions). All are
   PROCEDUREs with no exception handler, so the batch refreshes they
   call can COMMIT per month. Non-deterministic on purpose.
   ============================================================= */

/* ---- one random report, weighted by the chaos dial (0-100) ---- */
CREATE OR REPLACE PROCEDURE loadgen.usp_RandomReport(p_chaos int DEFAULT 15)
LANGUAGE plpgsql AS $$
DECLARE
    v_year int  := 1995 + floor(random()*22)::int;
    v_pk   bigint := (SELECT product_key  FROM dw.dim_product     ORDER BY random() LIMIT 1);
    v_ck   bigint := (SELECT customer_key FROM dw.agg_customer_ltv ORDER BY random() LIMIT 1);
    v_cc   char(2) := (SELECT country_code FROM dw.dim_geography   ORDER BY random() LIMIT 1);
    v_roll int := 1 + floor(random()*100)::int;
    v_which int;
BEGIN
    IF v_roll <= p_chaos THEN
        v_which := 1 + floor(random()*5)::int;
        IF    v_which = 1 THEN PERFORM batch.drain(rpt.usp_ToddsSalesReport(v_year));
        ELSIF v_which = 2 THEN PERFORM batch.drain(rpt.usp_MonthlyNumberForBoss());
        ELSIF v_which = 3 THEN PERFORM batch.drain(rpt.usp_FindGamesLikeThis('the'));
        ELSIF v_which = 4 THEN PERFORM batch.drain(rpt.usp_WhoBoughtWhat(v_pk));
        ELSE                   PERFORM batch.drain(rpt.usp_CustomerSearch(v_cc));
        END IF;
    ELSE
        v_which := 1 + floor(random()*8)::int;
        IF    v_which = 1 THEN PERFORM batch.drain(rpt.usp_rpt_SalesByPlatform());
        ELSIF v_which = 2 THEN PERFORM batch.drain(rpt.usp_rpt_TopSellers(25));
        ELSIF v_which = 3 THEN PERFORM batch.drain(rpt.usp_rpt_SalesByRegion(v_year));
        ELSIF v_which = 4 THEN PERFORM batch.drain(rpt.usp_rpt_TopSpenders(25, v_cc));
        ELSIF v_which = 5 THEN PERFORM batch.drain(rpt.usp_rpt_ReviewSentimentTrend());
        ELSIF v_which = 6 THEN PERFORM batch.drain(rpt.usp_rpt_WagesPctRevenue());
        ELSIF v_which = 7 THEN PERFORM batch.drain(rpt.usp_rpt_CustomerLTV(v_ck));
        ELSE                   PERFORM batch.drain(rpt.usp_rpt_ChannelMix(v_year));
        END IF;
    END IF;
END $$;

/* ---- a simulated analyst session: N random reports ---- */
CREATE OR REPLACE PROCEDURE loadgen.usp_AnalystSession(p_iterations int DEFAULT 10, p_chaos int DEFAULT 15)
LANGUAGE plpgsql AS $$
DECLARE v_i int := 0;
BEGIN
    WHILE v_i < p_iterations LOOP
        CALL loadgen.usp_RandomReport(p_chaos);
        v_i := v_i + 1;
    END LOOP;
END $$;

/* ---- write load: reprocess the reporting layer (idempotent) ---- */
CREATE OR REPLACE PROCEDURE loadgen.usp_BatchCycle(p_what varchar(20) DEFAULT 'rollups')
LANGUAGE plpgsql AS $$
BEGIN
    IF    p_what = 'everything' THEN CALL batch.usp_refresh_everything(false);
    ELSIF p_what = 'facts'      THEN CALL batch.usp_refresh_all_facts(false);
    ELSE                             CALL batch.usp_refresh_all_rollups();
    END IF;
END $$;

/* ---- named chaos presets — the load generator's mood dial ---- */
CREATE OR REPLACE PROCEDURE loadgen.usp_ChaosPreset(p_preset varchar(20) DEFAULT 'QuietTuesday')
LANGUAGE plpgsql AS $$
DECLARE v_pk bigint;
BEGIN
    IF p_preset = 'MonthEnd' THEN
        PERFORM batch.drain(rpt.usp_rpt_PnL(NULL));
        PERFORM batch.drain(rpt.usp_rpt_WagesPctRevenue());
        PERFORM batch.drain(rpt.usp_rpt_WarehouseVsFinance());
        PERFORM batch.drain(rpt.usp_MonthlyNumberForBoss());   -- the slow one the boss insists on
        CALL loadgen.usp_AnalystSession(8, 25);
    ELSIF p_preset = 'BlackFriday' THEN
        PERFORM batch.drain(rpt.usp_rpt_SalesByPlatform());
        PERFORM batch.drain(rpt.usp_rpt_SalesByRegion(NULL));
        PERFORM batch.drain(rpt.usp_rpt_ChannelMix(NULL));
        PERFORM batch.drain(rpt.usp_rpt_TrafficByDay());
        CALL loadgen.usp_AnalystSession(20, 20);
    ELSIF p_preset = 'ToddRanAReport' THEN
        v_pk := (SELECT product_key FROM dw.dim_product ORDER BY random() LIMIT 1);
        PERFORM batch.drain(rpt.usp_ToddsSalesReport(NULL));   -- SELECT *, UDF per row, full scan
        PERFORM batch.drain(rpt.usp_GetTopCustomersSLOW(20));  -- the cursor
        PERFORM batch.drain(rpt.usp_WhoBoughtWhat(v_pk));
    ELSE  -- QuietTuesday
        CALL loadgen.usp_AnalystSession(5, 5);
    END IF;
END $$;
