# Nick's Gaming Emporium

A deterministic generator that builds a large, realistic **synthetic database** —
the 30-year sales history (1986–2016) of a fictional video-game retailer — for
either **SQL Server** or **PostgreSQL**. It is designed as a richer, more
intuitive alternative to abstract benchmark schemas: real-looking products,
customers, shops, staff, payments, trade-ins, an online store, and (on SQL
Server) a full reporting/analytics warehouse.

Everything is generated from a fixed seed, so the same tier always produces the
**same database, byte for byte** — on any machine, any number of times, and (for
the layers both engines share) the **same data across SQL Server and
PostgreSQL**.

> 🕹️ There's a companion **fan site** for the (fictional) Emporium — a 1998-era
> tribute page telling the company's story:
> [ukhype83-dev.github.io/nge-fansite](https://ukhype83-dev.github.io/nge-fansite/)

## Two backends

| Layer | SQL Server | PostgreSQL |
|---|:---:|:---:|
| **OLTP** (`dbo`, `hr`, `finance`) — shops, catalogue, customers, staff, transactions, payments, trade-ins, finance roll-ups | ✅ | ✅ |
| **Web / community** (`web`) — accounts, reviews, comments, votes, clickstream | ✅ | ✅ |
| **Data warehouse** (`dw`) — dimensional model + ETL (`batch`) | ✅ | ✅ |
| **Reporting procedures** (`rpt`, incl. the tuning-lab "bad" procs) | ✅ | ✅ |
| **Workload generator** (`loadgen`) + batch jobs/maintenance | ✅ | ✅ |

The OLTP + web layers are generated from the same deterministic engine on both
backends, so a SQL Server database and a PostgreSQL database of the same tier
hold the **same rows** (aligned by primary key when built the same way — see
[Reproducibility](#build-speed---vusers-your-mileage-will-vary)). The data
warehouse (the dimensional model plus the `batch` ETL that populates it) now
builds on both backends too — on PostgreSQL it is a rowstore + BRIN design
(no columnstore, which is deliberately a "same query, two engines, different
physical design" teaching point). The **reporting** procedure library (`rpt` —
~190 report procedures, including the deliberately-awful tuning-lab set) is now
on PostgreSQL too, as `refcursor`-returning functions (call one, then `FETCH`
from the cursor it returns). The **workload generator** (`loadgen`) and the
overnight batch jobs + maintenance procedures are on PostgreSQL as well — so the
whole stored-procedure library runs on both engines, bar a few SQL-Server
storage internals with no Postgres equivalent (the columnstore build/rebuild
procedures; the `sys.dm_db_*` DMV monitoring reports are rewritten to their
`pg_stat_*` counterparts).

## Tiers

Pick a size with `--tier`. The name is the approximate footprint of the finished
SQL Server database (OLTP + web + data warehouse):

| Tier     | Size    | Transactions | Typical build time |
|----------|--------:|-------------:|--------------------|
| `tiny`   | ~8 GB   | ~3 M         | ~15 min            |
| `small`  | ~40 GB  | ~16 M        | ~1 hour            |
| `medium` | ~300 GB | ~145 M       | ~10 hours          |
| `large`  | ~2.4 TB | ~1.4 B       | ~1–2 days          |

`tiny` and `small` are compact extracts intended for development and learning.
`medium` and `large` are the full-scale datasets. On PostgreSQL the OLTP + web
footprint is somewhat **larger** than the SQL Server figure above (the rowstore
has no columnstore compression, and there's no warehouse yet).

## Requirements

- **Go 1.25+** (to build the generator)
- **SQL Server 2016 or later** (the warehouse uses columnstore indexes), **or
  PostgreSQL 14 or later**
- Enough free disk for your chosen tier (see the table above)

## Quick start

**1. Build the generator** (from `simulator/`):

```bash
cd simulator
go build -o build_emporium ./cmd/build_emporium
```

Then pick your backend below. One command builds the whole database. `--emit`
defaults to `full`, so you can leave it off.

### SQL Server (full stack, incl. warehouse)

Create an empty target database (adjust the file paths to your disks):

```sql
CREATE DATABASE nge_tiny ON PRIMARY
  (NAME = nge_tiny,     FILENAME = 'D:\Data\nge_tiny.mdf',    SIZE = 2048MB, FILEGROWTH = 1024MB)
  LOG ON
  (NAME = nge_tiny_log, FILENAME = 'D:\Log\nge_tiny_log.ldf', SIZE = 1024MB, FILEGROWTH = 512MB);
```

Build it:

```bash
./build_emporium \
  --load-mssql "sqlserver://user:password@host:1433?database=nge_tiny" \
  --init-schema --recovery-simple --tier tiny --vusers 8
```

The full pipeline runs end to end:

1. **OLTP** — shops, catalogue, customers, staff, transactions, payments, trade-ins
2. **Indexes** — nonclustered indexes (built after load, for speed)
3. **Web** — accounts, reviews, comments, votes, page-view clickstream
4. **Warehouse** — dimensional model, line-grain facts, rollups, columnstore
5. **ETL** — populates the warehouse from the OLTP data
6. **Validation** — reconciles the warehouse against source and prints PASS/FAIL

A successful run **exits 0**; a validation failure exits non-zero and reports
which check failed.

### PostgreSQL (OLTP + finance + web)

`CREATE DATABASE` can't run inside a transaction, so create the database with a
single-statement file:

```bash
printf 'CREATE DATABASE nge_tiny;\n' > create.sql
./build_emporium --load-postgres "postgres://user:password@host:5432/postgres" --deploy-sql create.sql
```

Build it — one command applies the schema and loads OLTP + finance + indexes + web:

```bash
./build_emporium \
  --load-postgres "postgres://user:password@host:5432/nge_tiny" \
  --init-schema --tier tiny
```

The full pipeline runs OLTP → indexes → web → **data warehouse → ETL →
validation** (the same six phases as SQL Server), ending in the reconciliation
gate. Notes for PostgreSQL:

- The OLTP transaction load runs **serially** (the parallel path is SQL Server
  only), so it's the slow phase on the big tiers; the web clickstream still runs
  in parallel. `--vusers` therefore tunes only the clickstream — the data is
  identical regardless.
- The warehouse is rowstore + BRIN (no columnstore); the `batch` ETL populates
  it and the build finishes with the same reconciliation checks as SQL Server.
  (The `rpt`/`loadgen` procedure libraries are not on PostgreSQL yet.)
- Don't re-run a build into an already-populated database to "resume" — drop and
  recreate, then build clean.

## Downloads (prebuilt databases)

Don't want to build a terabyte yourself? Prebuilt database backups for each tier
are published on the **[Releases page](https://github.com/ukhype83-dev/nicks-gaming-emporium/releases)**.

Each release is tied to the exact generator version that produced it, and lists
a **SHA-256** for every file so you can verify a large download landed intact.
Restore a SQL Server backup with:

```sql
RESTORE DATABASE nge_tiny FROM DISK = 'D:\Downloads\nge_tiny.bak'
  WITH MOVE 'nge_tiny'     TO 'D:\Data\nge_tiny.mdf',
       MOVE 'nge_tiny_log' TO 'D:\Log\nge_tiny_log.ldf';
```

Prebuilt files are SQL Server backups today; PostgreSQL dumps (`pg_restore`) are
planned alongside the PostgreSQL warehouse work. Building from source (above) is
always the zero-cost option and works for both backends now. For a given seed
and `--vusers` it produces a byte-identical database; the data and all aggregate
results are identical regardless of `--vusers` (only the build-order surrogate
ids differ). The downloads are purely a convenience for the larger tiers.

### Useful flags

| Flag                  | Purpose |
|-----------------------|---------|
| `--tier`              | `tiny` \| `small` \| `medium` \| `large` |
| `--load-mssql`        | SQL Server target DSN: `sqlserver://user:pass@host:1433?database=NAME` |
| `--load-postgres`     | PostgreSQL target DSN: `postgres://user:pass@host:5432/NAME` |
| `--init-schema`       | apply the table schema before loading (use on an empty database) |
| `--recovery-simple`   | (SQL Server) set SIMPLE recovery for faster bulk load |
| `--vusers`            | parallel data-load workers. See **Build speed** below — more isn't always faster, and it changes id reproducibility. |
| `--emit`              | defaults to `full` (build everything — omit it for the normal case). `oltp` builds only the OLTP base; or name a single layer, e.g. `web`. (`all` is a back-compat alias for `full`.) |
| `--deploy-sql`        | run a single SQL file against the target (used to `CREATE DATABASE` on PostgreSQL) |
| `--pg-call`           | run one autocommit statement against `--load-postgres` and exit — e.g. `--pg-call "CALL loadgen.usp_BatchCycle('facts')"`. Needed for procedures that `COMMIT` (the batch/loadgen write jobs), which `--deploy-sql` can't run. |
| `--validate=false`    | (SQL Server) skip the final reconciliation gate |

The schema files applied by `--init-schema` are at the repo root:
`schema_v1_sqlserver.sql` / `schema_v1_sqlserver_indexes.sql` and
`schema_v1_postgres.sql` / `schema_v1_postgres_indexes.sql`.

### Build speed: `--vusers` (your mileage will vary)

`--vusers` sets how many workers load the data in parallel. On a machine with
plenty of cores, fast disks and RAM, more workers build faster. But on SQL
Server the parallel path falls back to a slower per-row insert — single-worker
mode (`--vusers 1`) uses SQL Server's fast bulk-copy path — so on a modest or
shared box `--vusers 1` can actually be the **fastest** option. Try a couple of
values on the `tiny` tier first; your mileage will vary with cores, disk and RAM.
(On PostgreSQL the OLTP load is serial regardless, so `--vusers` only affects the
web clickstream.)

Reproducibility note: surrogate ids (`transaction_id`, `page_view_id`, …) are
numbered in load order, so different `--vusers` values produce the **same data
with different id labels**. Build with a fixed `--vusers` (or `--vusers 1`) if
you need byte-identical reproducibility, ids included — including matching
row-for-row **across the two backends** (build both with `--vusers 1`).

## What you get

- **OLTP schema** (`dbo`, `hr`, `finance`) — the operational database.
  *(SQL Server + PostgreSQL)*
- **Web/community schema** (`web`) — accounts, reviews, votes, clickstream.
  *(SQL Server + PostgreSQL)*
- **Data warehouse** (`dw`) — conformed dimensions, line-grain fact tables, a
  wide denormalised table, and rollups, populated by the reprocessable `batch`
  ETL. *(SQL Server + PostgreSQL — columnstore on SQL Server, rowstore + BRIN
  on PostgreSQL)*
- **Reporting procedures** (`rpt`) — ~190 report/dashboard procedures over the
  warehouse, including a deliberately-awful tuning-lab set (scalar-UDF taxes,
  non-SARGable predicates, RBAR cursors, views-on-views) whose bad performance
  is preserved on purpose. *(SQL Server + PostgreSQL — on PostgreSQL they are
  `refcursor`-returning functions)*
- **Workload generator** (`loadgen`) — procedures that drive concurrent read +
  batch workload over the above, plus in-character overnight batch jobs and DBA
  maintenance procedures (`batch`). *(SQL Server + PostgreSQL)*

The data is internally consistent (foreign keys enforced, financials reconcile)
and reproducible from the seed, so it is well suited to SQL learning,
performance tuning, and analytics/BI practice at a range of scales — including
**cross-engine** work, since the OLTP and web layers hold the same data on both
SQL Server and PostgreSQL.

## Notes

- The generator writes only to the target database you specify; it never
  modifies these source files.
- Rebuilding the same tier produces an identical database — handy for teaching
  and for reproducing issues.

## License

See [LICENSE.md](LICENSE.md). Seed-data attributions are in
[seed_data/LICENSES.md](seed_data/LICENSES.md).
