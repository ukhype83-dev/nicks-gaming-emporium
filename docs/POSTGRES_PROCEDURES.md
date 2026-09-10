# PostgreSQL stored procedures — cheatsheet

| Schema | Call with |
|---|---|
| `rpt.*` — reports | `SELECT` (returns a `refcursor`) |
| `batch.*` — ETL, jobs, maintenance | `CALL` |
| `loadgen.*` — workload generator | `CALL` |

| PostgreSQL | SQL Server |
|---|---|
| `SELECT rpt.usp_x(2016)` → refcursor, then `FETCH` | `EXEC rpt.usp_x @year = 2016` |
| `CALL batch.usp_y(...)` | `EXEC batch.usp_y ...` |
| `SETOF refcursor` | multiple result sets |
| lowercase / unquoted names | case-insensitive names |
| `public` schema | `dbo` schema |

## Reports — `rpt.*`

psql:

```sql
BEGIN;
SELECT rpt.usp_rpt_salesbyregion(2016) AS cur \gset
FETCH ALL FROM :"cur";
COMMIT;
```

Other clients (DBeaver, pgAdmin, drivers) — auto-commit off:

```sql
SELECT rpt.usp_rpt_salesbyregion(2016);   -- returns a cursor name, e.g. <unnamed portal 2>
FETCH ALL FROM "<unnamed portal 2>";      -- the exact name shown
COMMIT;
```

Multi-result (`SETOF refcursor`) — one cursor per result set:

```sql
BEGIN;
SELECT rpt.usp_geteverythingaboutacustomer(1);
FETCH ALL FROM "<unnamed portal 1>";
FETCH ALL FROM "<unnamed portal 2>";
COMMIT;
```

## Jobs / ETL / workload — `batch.*`, `loadgen.*`

```sql
CALL batch.usp_refresh_everything(true);
CALL loadgen.usp_batchcycle('facts');
```

CLI: `build_emporium --load-postgres "<dsn>" --pg-call "CALL batch.usp_refresh_everything(true)"`

Report as load (rows discarded):

```sql
BEGIN; SELECT batch.drain(rpt.usp_rpt_salesbyregion(2016)); COMMIT;
```

## Notes

- Names are lowercase/unquoted: `rpt.usp_rpt_salesbyregion`, not `"usp_rpt_SalesByRegion"`.
- Reports need a transaction; `CALL`s must not be wrapped in one (they commit internally).
- Cursor name (`<unnamed portal N>`) increments per open — use the one shown.
- Smallint params: cast if unresolved — `rpt.x(2016::smallint)`.
- `\gset` / `:"cur"` are psql-only.

## List procedures

```sql
SELECT n.nspname, p.proname,
       CASE p.prokind WHEN 'p' THEN 'CALL' ELSE 'SELECT' END AS how,
       pg_get_function_identity_arguments(p.oid) AS args
FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname IN ('rpt', 'batch', 'loadgen') ORDER BY 1, 2;
```
