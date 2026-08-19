-- Build/refresh the whole reporting layer end to end: dims → facts → rollups.
-- Idempotent — safe to re-run. @full=1 is a full reprocess.
EXEC batch.usp_refresh_everything @full = 1;
GO
