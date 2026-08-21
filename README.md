# Nick's Gaming Emporium

A deterministic generator that builds a large, realistic **synthetic SQL Server
database** — the 30-year sales history (1986–2016) of a fictional video-game
retailer. It is designed as a richer, more intuitive alternative to abstract
benchmark schemas: real-looking products, customers, shops, staff, payments,
trade-ins, an online store, and a full reporting/analytics warehouse.

Everything is generated from a fixed seed, so the same tier always produces the
**same database, byte for byte** — on any machine, any number of times.

> 🕹️ There's a companion **fan site** for the (fictional) Emporium — a 1998-era
> tribute page telling the company's story:
> [ukhype83-dev.github.io/nge-fansite](https://ukhype83-dev.github.io/nge-fansite/)

## Tiers

Pick a size with `--tier`. The name is the approximate footprint of the finished
database (OLTP + web + data warehouse):

| Tier     | Size    | Transactions | Typical build time |
|----------|--------:|-------------:|--------------------|
| `tiny`   | ~8 GB   | ~3 M         | ~15 min            |
| `small`  | ~40 GB  | ~16 M        | ~1 hour            |
| `medium` | ~300 GB | ~145 M       | ~10 hours          |
| `large`  | ~2.4 TB | ~1.4 B       | ~1–2 days          |

`tiny` and `small` are compact extracts intended for development and learning.
`medium` and `large` are the full-scale datasets.

## Requirements

- **Go 1.25+** (to build the generator)
- **SQL Server 2016 or later** (the schema uses columnstore indexes)
- Enough free disk for your chosen tier (see the table above)

## Quick start

**1. Build the generator** (from `simulator/`):

```bash
cd simulator
go build -o build_emporium ./cmd/build_emporium
```

**2. Create an empty target database** on your SQL Server. Adjust the file paths
to your disks:

```sql
CREATE DATABASE nge_tiny ON PRIMARY
  (NAME = nge_tiny,     FILENAME = 'D:\Data\nge_tiny.mdf',    SIZE = 2048MB, FILEGROWTH = 1024MB)
  LOG ON
  (NAME = nge_tiny_log, FILENAME = 'D:\Log\nge_tiny_log.ldf', SIZE = 1024MB, FILEGROWTH = 512MB);
```

**3. Build the database** — one command builds everything and validates it.
`--emit` defaults to `full` (build the whole thing), so you can leave it off:

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

Building from source (above) is always the zero-cost option. For a given seed
and `--vusers` it produces a byte-identical database; the data and all aggregate
results are identical regardless of `--vusers` (only the build-order surrogate
ids differ). The downloads are purely a convenience for the larger tiers.

### Useful flags

| Flag                  | Purpose |
|-----------------------|---------|
| `--tier`              | `tiny` \| `small` \| `medium` \| `large` |
| `--load-mssql`        | target DSN: `sqlserver://user:pass@host:1433?database=NAME` |
| `--init-schema`       | apply the table schema before loading (use on an empty database) |
| `--recovery-simple`   | set SIMPLE recovery for faster bulk load |
| `--vusers`            | parallel build workers (match to your CPU; 4–16 is typical) |
| `--emit`              | defaults to `full` (build everything — omit it for the normal case). `oltp` builds only the OLTP base; or name a single layer, e.g. `web`. (`all` is a back-compat alias for `full`.) |
| `--validate=false`    | skip the final reconciliation gate |

## What you get

- **OLTP schema** (`dbo`, `hr`, `finance`) — the operational database.
- **Web/community schema** (`web`) — accounts, reviews, votes, clickstream.
- **Data warehouse** (`dw`) — conformed dimensions, line-grain fact tables, a
  wide denormalised table, rollups, and columnstore indexes.
- **A stored-procedure library** (`rpt`, `batch`, `loadgen`) — reporting,
  reprocessable ETL, and workload-generation procedures over the warehouse.

The data is internally consistent (foreign keys enforced, financials reconcile)
and reproducible from the seed, so it is well suited to SQL learning,
performance tuning, and analytics/BI practice at a range of scales.

## Notes

- The generator writes only to the target database you specify; it never
  modifies these source files.
- Rebuilding the same tier produces an identical database — handy for teaching
  and for reproducing issues.

## License

See [LICENSE.md](LICENSE.md). Seed-data attributions are in
[seed_data/LICENSES.md](seed_data/LICENSES.md).
