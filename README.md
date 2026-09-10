# Nick's Gaming Emporium

A deterministic generator that builds a large, realistic **synthetic database** —
the 30-year sales history (1986–2016) of a fictional video-game retailer — for
either **SQL Server** or **PostgreSQL**. It is designed as a richer, more
intuitive alternative to abstract benchmark schemas: a **real video-game
catalogue** moving through synthetic shops, customers, staff, payments,
trade-ins, an online store, and a full reporting/analytics warehouse.

**The product catalogue is real.** Every game in the database is an actual
release — real titles, platforms, and release dates — compiled from public
sources (see [LICENSE.md](LICENSE.md)). Everything *around* those products — the
retailer, its shops, customers, staff, transactions, and finances — is entirely
fictional and generated, and is not affiliated with or endorsed by any real
company.

Because the catalogue is real, it isn't trimmed to the company's lifespan: it
runs from 1977 into the mid-2020s, so roughly **6,200 games released *after*
NGE's September 2016 closure** sit in the `releases` table with **no sales
against them**. All transactional data — every transaction, review, and page
view — still stops at the 2016 shutdown. The catalogue is simply a reference
dimension that outlives the (fictional) business, the way a real product master
would.

Everything is generated from a fixed seed, so the same tier always produces the
**same database, byte for byte** — on any machine, any number of times, and (for
the layers both engines share) the **same data across SQL Server and
PostgreSQL**.

> 🕹️ There's a companion site for the (fictional) Emporium — a **documentary**
> telling the company's 1986–2016 rise and fall:
> [ukhype83-dev.github.io/nge-fansite](https://ukhype83-dev.github.io/nge-fansite/).
> The original **1998 fan tribute** is preserved alongside it at
> [1998.html](https://ukhype83-dev.github.io/nge-fansite/1998.html).

<div align="center">
<table>
  <tr>
    <td align="center"><img src="docs/nge-logo-1986.jpg" width="230" alt="NGE logo, 1986 — hand-painted walnut sign with a butterscotch as the apostrophe"></td>
    <td align="center"><img src="docs/nge-logo-1997.jpg" width="230" alt="NGE logo, 1997 — chrome-and-teal corporate NGE with a swoosh"></td>
    <td align="center"><img src="docs/nge-logo-2015.jpg" width="230" alt="NGE logo, 2015 — flat lowercase nge. with a gold dot"></td>
  </tr>
  <tr>
    <td align="center"><sub><b>1986</b> · painted by hand</sub></td>
    <td align="center"><sub><b>1997</b> · after the IPO</sub></td>
    <td align="center"><sub><b>2015</b> · the last rebrand</sub></td>
  </tr>
</table>
<sub><i>One company, three signs over the door — the whole rise and fall is in the logo. (See the <a href="https://ukhype83-dev.github.io/nge-fansite/">documentary</a>.)</i></sub>
</div>

## Two backends

| Layer | SQL Server | PostgreSQL |
|---|:---:|:---:|
| **OLTP** (`dbo`/`public`, `hr`, `finance`) — shops, catalogue, customers, staff, transactions, payments, trade-ins, finance roll-ups | ✅ | ✅ |
| **Web / community** (`web`) — accounts, reviews, comments, votes, clickstream | ✅ | ✅ |
| **Data warehouse** (`dw`) — dimensional model + ETL (`batch`) | ✅ | ✅ |
| **Reporting procedures** (`rpt`, incl. the tuning-lab "bad" procs) | ✅ | ✅ |
| **Workload generator** (`loadgen`) + batch jobs/maintenance | ✅ | ✅ |

The OLTP + web layers are generated from the same deterministic engine on both
backends, so a SQL Server database and a PostgreSQL database of the same tier
hold the **same rows**, aligned by primary key (see
[Reproducibility](#reproducibility)). One deliberate naming difference: the OLTP
core tables live in each engine's **default schema** — `dbo` on SQL Server,
`public` on PostgreSQL — while every other schema (`hr`, `web`, `finance`, `dw`,
`rpt`, `batch`, `loadgen`) keeps the same name on both. So a table SQL Server
calls `dbo.customers` is `public.customers` on PostgreSQL. The data
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

| Tier     | Size    | Transactions | Build (SQL Server) | Build (PostgreSQL) |
|----------|--------:|-------------:|-------------------:|-------------------:|
| `tiny`   | ~8 GB   | ~3 M         | ~4 min             | ~6 min             |
| `small`  | ~40 GB  | ~16 M        | ~25 min            | ~36 min            |
| `medium` | ~300 GB | ~145 M       | ~4h 40m            | ~12h               |
| `large`  | ~2.4 TB | ~1.4 B       | ~42h 30m           | ~100h              |

Build times are indicative — from the project's reference host — and scale with
your hardware. Both engines load the transaction phase serially; PostgreSQL is
just slower to build at the larger tiers. The prebuilt
[downloads](#downloads-prebuilt-databases) skip the build entirely.

`tiny` and `small` are compact extracts intended for development and learning.
`medium` and `large` are the full-scale datasets. PostgreSQL builds the **full
stack** as well (OLTP + web + warehouse), and a finished database is a
comparable total size to SQL Server — `large` is ~2.4 TB on both — though the
split differs: PostgreSQL's rowstore OLTP runs a little smaller and its
rowstore + BRIN warehouse a little larger than SQL Server's columnstore.

## Requirements

- **Go 1.25+** (to build the generator)
- **SQL Server 2016 SP1 or later** — the stored-procedure library is deployed
  with `CREATE OR ALTER` (introduced in 2016 SP1) and the warehouse uses
  updatable nonclustered columnstore (introduced in 2016) — **or PostgreSQL 14
  or later**
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
  --init-schema --recovery-simple --tier tiny
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

### PostgreSQL (full stack, incl. warehouse)

`CREATE DATABASE` can't run inside a transaction, so create the database with a
single-statement file:

```bash
printf 'CREATE DATABASE nge_tiny;\n' > create.sql
./build_emporium --load-postgres "postgres://user:password@host:5432/postgres" --deploy-sql create.sql
```

Build it — one command applies the schema and builds the full stack:

```bash
./build_emporium \
  --load-postgres "postgres://user:password@host:5432/nge_tiny" \
  --init-schema --tier tiny
```

The full pipeline runs OLTP → indexes → web → **data warehouse → ETL →
validation** (the same six phases as SQL Server), ending in the reconciliation
gate. Notes for PostgreSQL:

- The OLTP transaction load runs **serially** (as on SQL Server), so it's the
  slow phase on the big tiers; the web clickstream still runs in parallel across
  CPU cores.
- The warehouse is rowstore + BRIN (no columnstore); the `batch` ETL populates
  it and the build finishes with the same reconciliation checks as SQL Server.
  The `rpt` reporting library and the `loadgen` workload generator run on
  PostgreSQL too (see [Two backends](#two-backends)) — so the full stack builds
  on either engine.
- Don't re-run a build into an already-populated database to "resume" — drop and
  recreate, then build clean.

## Downloads (prebuilt databases)

Don't want to build a terabyte yourself? Prebuilt backups are published for
**both engines** on **`downloads.nge-data.com`** (grab the per-file links below) —
a SQL Server backup (`.bak`, `v0.2.0`) and a PostgreSQL custom-format dump
(`.dump`) per tier.

| Tier | SQL Server (`.bak`) | PostgreSQL (`.dump`) |
|---|---|---|
| `tiny`   | [nge-tiny-mssql-v0.2.0.bak](https://downloads.nge-data.com/nge-tiny-mssql-v0.2.0.bak)     | [nge_tiny.dump](https://downloads.nge-data.com/nge_tiny.dump)     |
| `small`  | [nge-small-mssql-v0.2.0.bak](https://downloads.nge-data.com/nge-small-mssql-v0.2.0.bak)   | [nge_small.dump](https://downloads.nge-data.com/nge_small.dump)   |
| `medium` | [nge-medium-mssql-v0.2.0.bak](https://downloads.nge-data.com/nge-medium-mssql-v0.2.0.bak) | [nge_medium.dump](https://downloads.nge-data.com/nge_medium.dump) |
| `large`  | striped ×3: [1of3](https://downloads.nge-data.com/nge-large-mssql-v0.2.0-1of3.bak) · [2of3](https://downloads.nge-data.com/nge-large-mssql-v0.2.0-2of3.bak) · [3of3](https://downloads.nge-data.com/nge-large-mssql-v0.2.0-3of3.bak) | [nge_large.dump](https://downloads.nge-data.com/nge_large.dump) |

Verify with the published checksums —
**[SQL Server SHA256SUMS](https://downloads.nge-data.com/nge-v0.2.0-SHA256SUMS.txt)**
and **[PostgreSQL SHA256SUMS](https://downloads.nge-data.com/nge-pg-SHA256SUMS.txt)** —
each lists a SHA-256 for every file, so you can confirm a large download landed
intact (check just the tiers you grabbed):

```bash
# Linux/macOS — --ignore-missing verifies the files present and skips the rest
sha256sum -c --ignore-missing nge-pg-SHA256SUMS.txt
# Windows — compare the printed hash to the matching line in the SUMS file
Get-FileHash nge_large.dump -Algorithm SHA256
```

### Restore — SQL Server

Single-file tiers (`tiny` / `small` / `medium`):

```sql
RESTORE DATABASE nge_tiny FROM DISK = 'D:\Downloads\nge-tiny-mssql-v0.2.0.bak'
  WITH MOVE 'nge_tiny'     TO 'D:\Data\nge_tiny.mdf',
       MOVE 'nge_tiny_log' TO 'D:\Log\nge_tiny_log.ldf';
```

The `large` backup is **striped across three files** — list all three in one
`RESTORE` (order doesn't matter):

```sql
RESTORE DATABASE nge_large FROM
    DISK = 'D:\Downloads\nge-large-mssql-v0.2.0-1of3.bak',
    DISK = 'D:\Downloads\nge-large-mssql-v0.2.0-2of3.bak',
    DISK = 'D:\Downloads\nge-large-mssql-v0.2.0-3of3.bak'
  WITH MOVE 'nge_large'     TO 'D:\Data\nge_large.mdf',
       MOVE 'nge_large_log' TO 'D:\Log\nge_large_log.ldf';
```

(If the logical file names differ, `RESTORE FILELISTONLY FROM DISK = '…'` lists them.)

### Restore — PostgreSQL

The dumps are custom-format, so restore them with `pg_restore` — in parallel
(`-j`), which works on a single-file dump too. **Create the target database
first**, restore into it, then **`ANALYZE`**:

```bash
# 1. create an empty database — must be UTF-8-encoded; locale is your choice (see note below)
createdb nge_large

# 2. restore in parallel (-j = worker count; tune to your cores/RAM)
pg_restore -d nge_large -j 4 -v nge_large.dump

# 3. IMPORTANT: rebuild planner statistics — dumps don't carry them
psql -d nge_large -c "ANALYZE;"
```

> **Why the `ANALYZE`?** `pg_dump` stores data but not planner statistics, so
> without it the warehouse's BRIN indexes go unused and analytical queries fall
> back to full table scans. It takes a few minutes and makes the database
> query-ready. (On a fresh from-source build the ETL analyzes itself; a restore
> is the one path where you must do it by hand.)

> **Locale note.** The one thing that must match the dumps is the **encoding —
> create the target as UTF-8**. Beyond that the databases carry **no hardcoded
> collation** (every text column inherits the database default), so the
> **locale/collation is a free choice**: `en_US.UTF-8` / `en_GB.UTF-8` on
> Linux/macOS, `English_United States.1252` on Windows (the database is still
> UTF-8-encoded — the `.1252` is only the Windows locale's code page, not the
> database encoding), or `C` for byte-identical sort order everywhere.
> Case-sensitivity and every reconciliation figure are preserved regardless of
> which you pick (they are all deterministic, case-sensitive collations). What
> *does* vary by locale is linguistic text handling — sort order, and
> case-folding such as `upper()`/`lower()` on non-ASCII — but that changes only
> query presentation, never the stored data or the reconciliation totals.

### Verify a restore (optional)

[**`FINGERPRINTS.md`**](FINGERPRINTS.md) publishes a 20-metric **fingerprint**
for every tier — row counts and money totals across the OLTP, web, and warehouse
layers, with the exact query for each engine. After restoring, run the query and
confirm your database matches its row: that proves the restore is byte-faithful,
not merely that the file downloaded.

Building from source (above) is always the zero-cost option and works for both
backends. For a given seed it produces a byte-identical database — every time, on
any machine, and identically across both engines. The downloads are purely a
convenience for the larger tiers.

### Useful flags

| Flag                  | Purpose |
|-----------------------|---------|
| `--tier`              | `tiny` \| `small` \| `medium` \| `large` |
| `--load-mssql`        | SQL Server target DSN: `sqlserver://user:pass@host:1433?database=NAME` |
| `--load-postgres`     | PostgreSQL target DSN: `postgres://user:pass@host:5432/NAME` |
| `--init-schema`       | apply the table schema before loading (use on an empty database) |
| `--recovery-simple`   | (SQL Server) optionally set SIMPLE recovery so the loader's `TABLOCK` bulk-copy can be minimally logged. |
| `--emit`              | defaults to `full` (build everything — omit it for the normal case). `oltp` builds only the OLTP base; or name a single layer, e.g. `web`. (`all` is a back-compat alias for `full`.) |
| `--deploy-sql`        | run a single SQL file against the target (used to `CREATE DATABASE` on PostgreSQL) |
| `--pg-call`           | run one autocommit statement against `--load-postgres` and exit — e.g. `--pg-call "CALL loadgen.usp_BatchCycle('facts')"`. Needed for procedures that `COMMIT` (the batch/loadgen write jobs), which `--deploy-sql` can't run. |
| `--validate=false`    | skip the final reconciliation gate (either engine) |

The schema files applied by `--init-schema` are at the repo root:
`schema_v1_sqlserver.sql` / `schema_v1_sqlserver_indexes.sql` and
`schema_v1_postgres.sql` / `schema_v1_postgres_indexes.sql`.

### Reproducibility

The build is fully deterministic. Given the same seed and tier, it produces a
**byte-identical** database — every time, on any machine — and the same rows,
aligned by primary key, **across both backends**: a SQL Server database and a
PostgreSQL database of the same tier are literal twins for every layer the two
engines share. All surrogate ids (`transaction_id`, `page_view_id`, …) are
assigned in a fixed order, so they match too.

## First queries

Once a database is built or restored, here are a few queries to get oriented.
The two warehouse examples run **identically on both engines**.

**Top-selling games** — the warehouse ships a product rollup, so this reads a
small pre-aggregated table instead of scanning the fact rows:

```sql
SELECT dp.title, SUM(app.units_sold) AS units
FROM dw.agg_product_performance app
JOIN dw.dim_product dp ON dp.product_key = app.product_key
WHERE dp.release_id IS NOT NULL          -- games only; drop this to include hardware
GROUP BY dp.title
ORDER BY units DESC
FETCH FIRST 10 ROWS ONLY;                -- ANSI top-N; works on both engines
```

Hardware tops the chart by *revenue* while games top it by *units* — a real
retailer's shape. Try it without the `release_id` filter and order by
`SUM(app.revenue_usd)` to see the consoles.

**Sales by month** — straight from the warehouse's monthly rollup:

```sql
SELECT year_month, tx_count, units, gross_revenue_usd
FROM dw.agg_sales_by_month
ORDER BY year_month;   -- 1986-08 through 2016-09, the company's whole life
```

**Run a reporting procedure** — the `rpt` library has ~190 of them. The call
differs by engine:

```sql
-- SQL Server: call it directly
EXEC rpt.usp_rpt_SalesByRegion 2016;

-- PostgreSQL: the function returns a cursor; capture its name with psql's \gset
-- and fetch from that, in one transaction. (GUI tools: see the proc guide below.)
BEGIN;
SELECT rpt.usp_rpt_salesbyregion(2016) AS cur \gset
FETCH ALL FROM :"cur";
COMMIT;
```

For the full calling reference on each engine — invoking reports, jobs, and the
workload generator, plus the gotchas coming from the *other* engine — see Running
the stored procedures on
[**PostgreSQL**](docs/POSTGRES_PROCEDURES.md) or [**SQL Server**](docs/SQLSERVER_PROCEDURES.md).

The table schema is in `schema_v1_sqlserver.sql` / `schema_v1_postgres.sql` at
the repo root; the warehouse and reporting SQL is under `sql/`. For the story
behind the data — the company, its people, and its slow-motion collapse — see the
[documentary](https://ukhype83-dev.github.io/nge-fansite/) (and the preserved
[1998 tribute](https://ukhype83-dev.github.io/nge-fansite/1998.html)).

## What you get

- **OLTP schema** (`dbo` on SQL Server / `public` on PostgreSQL, plus `hr`,
  `finance`) — the operational database. *(SQL Server + PostgreSQL)*
- **Web/community schema** (`web`) — accounts, reviews, comments, votes, clickstream.
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
