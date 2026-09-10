# SQL Server stored procedures — cheatsheet

| Schema | Call with |
|---|---|
| `rpt.*` — reports | `EXEC` (rows return directly) |
| `batch.*` — ETL, jobs, maintenance | `EXEC` |
| `loadgen.*` — workload generator | `EXEC` |

| SQL Server | PostgreSQL |
|---|---|
| `EXEC rpt.usp_x @year = 2016` → rows | `SELECT rpt.usp_x(2016)` → refcursor, then `FETCH` |
| `EXEC batch.usp_y ...` | `CALL batch.usp_y(...)` |
| multiple result sets | `SETOF refcursor` |
| case-insensitive names | lowercase / unquoted names |
| `dbo` schema | `public` schema |
| `[bracketed]` identifiers | `"quoted"` identifiers |

## Reports — `rpt.*`

```sql
EXEC rpt.usp_rpt_SalesByRegion @year = 2016;   -- named
EXEC rpt.usp_rpt_SalesByRegion 2016;           -- positional
EXEC rpt.usp_rpt_SalesByRegion;                -- defaults
```

Multi-result (several grids):

```sql
EXEC rpt.usp_GetTransaction @transaction_id = 1000;
```

## Jobs / ETL / workload — `batch.*`, `loadgen.*`

```sql
EXEC batch.usp_refresh_everything @full = 1;
EXEC loadgen.usp_BatchCycle 'facts';
```

## Notes

- Everything is `EXEC`; reports return rows directly (no cursor / `FETCH`).
- `dbo` is the default schema; other schema names match PostgreSQL.
- `GO` separates batches in SSMS / `sqlcmd` (between `CREATE PROCEDURE`s, not for `EXEC`).
- Identifiers are case-insensitive; quote with `[brackets]`.
- Query syntax: `TOP n` not `LIMIT n`, `GETDATE()` not `now()`.

## List procedures

```sql
SELECT s.name AS [schema], p.name,
       'EXEC ' + s.name + '.' + p.name AS how_to_call
FROM sys.procedures p JOIN sys.schemas s ON s.schema_id = p.schema_id
WHERE s.name IN ('rpt', 'batch', 'loadgen') ORDER BY s.name, p.name;
```

Params of one proc: `EXEC sp_help 'rpt.usp_rpt_SalesByRegion';`
