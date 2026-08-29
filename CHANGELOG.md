# Changelog

All notable changes to this project are documented here. It follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Each tagged release corresponds to a specific generator version. The prebuilt
database backups on the
[Releases page](https://github.com/ukhype83-dev/nicks-gaming-emporium/releases)
are produced by the generator at that tag, so a download and a from-source build
of the same tag are equivalent.

## [Unreleased]

### Added
- **PostgreSQL backend.** The OLTP (`public`, `hr`, `finance`) and web (`web`)
  layers can now be built on PostgreSQL as well as SQL Server, via
  `--load-postgres`. The generator is a single deterministic engine, so the two
  backends hold the **same data** for these layers — byte-identical, aligned by
  primary key. The OLTP core tables sit in Postgres's default `public` schema,
  matching where SQL Server uses its default `dbo` (so SQL Server's
  `dbo.customers` is `public.customers` on PostgreSQL); every other schema name
  is identical on both engines. New schema files
  `schema_v1_postgres.sql` / `schema_v1_postgres_indexes.sql` at the repo root,
  applied by `--init-schema`. The OLTP transaction load runs serially.
- **PostgreSQL data warehouse + ETL.** `--emit full` on PostgreSQL now also
  builds the `dw` dimensional model (11 dimensions, 3 line-grain fact tables, 7
  rollups, a wide denormalised table) and the reprocessable `batch` ETL that
  populates it, finishing with the same nine-check reconciliation gate as SQL
  Server (exit 0 on pass; counts and USD reconcile to source). The warehouse is
  a **rowstore + BRIN** design rather than columnstore — a deliberate
  "same query, two engines, different physical design" difference. New
  `--pg-run-etl` re-runs the DW ETL against an existing database.
- **PostgreSQL reporting procedures.** The full `rpt` reporting library (~190
  procedures, including the deliberately-awful tuning-lab set — scalar-UDF
  taxes, non-SARGable predicates, RBAR cursors, the `NOT IN`/NULL trap,
  views-on-views) is now on PostgreSQL as `refcursor`-returning functions (call
  one, then `FETCH` from the returned cursor; multi-result procedures return
  `SETOF refcursor`). The anti-patterns' bad performance is preserved on purpose
  (`VOLATILE` UDFs that Postgres won't inline, predicates kept non-SARGable);
  where one would *error* rather than merely scan on Postgres (the implicit
  text-vs-int till report), a minimal change keeps it runnable-but-slow.
- **PostgreSQL workload generator + batch jobs/maintenance.** The `loadgen`
  workload procedures, the in-character overnight batch jobs (supplier feeds,
  loyalty/tier recomputes, month-end close, statement runs) and the DBA
  maintenance procedures are now on PostgreSQL too — completing the
  stored-procedure library on both engines. `WAITFOR DELAY` → `pg_sleep`,
  `NEWID()` → `random()`, the CPU-burn loop uses `clock_timestamp()` (Postgres
  `now()` is transaction-fixed), and `loadgen` drains the reporting functions'
  cursors to generate real read load. The `sys.dm_db_*` DMV monitoring reports
  (index/columnstore/table health) are rewritten to their `pg_stat_*`
  equivalents. The only procedures not ported are the three columnstore
  build/drop/rebuild routines, which have no Postgres analogue (BRIN replaces
  them).
- `--pg-call "<sql>"` — run one statement against `--load-postgres` over the
  autocommit (simple) protocol and exit. Unlike `--deploy-sql` it does not wrap
  the statement in a transaction, so procedures that `COMMIT` — the batch
  refresh jobs and the `loadgen` write cycles — can be driven from the CLI (e.g.
  `--pg-call "CALL loadgen.usp_BatchCycle('facts')"`), which is how you exercise
  the workload generator on PostgreSQL without a client that manages autocommit.

### Changed
- **Accurate release dates; nothing shipped before release.** About 40% of the
  catalogue carried only a year-only `YYYY-01-01` fallback date. Real dates are
  now recovered for **8,178** of those titles — 5,458 from each game's own
  scraped date strings and regional columns, and 2,720 from Wikidata's
  structured publication dates (`P577`). Every correction is accepted only when
  its year matches the year already on record, so a wrong match can't change the
  data. Because the sales sampler gates on the full release date, this places
  those titles' transactions — and the reviews that follow real purchases — in
  realistic time. In addition, the two previously-ungated paths — unverified
  ("browsed") reviews and the page-view clickstream — now sample only titles
  already released at that moment, so **no review or page view ever references a
  game before it shipped**. Corrections live in a new data file,
  `seed_data/release_date_overrides.tsv`, applied at catalogue load. This shifts
  canonical OLTP + web data, so a rebuilt database differs from a prior one; it
  stays fully deterministic for a given seed.
- **Review-engine immersion pass.** Product reviews and comment threads now
  read truer to the game they are about:
  - **Physical medium is correct.** A review can no longer describe a cartridge
    game's "resurfaced disc" or a disc game's "cartridge pins / battery save".
    The medium is derived per platform, and format-specific lines are gated to
    it; medium-neutral lines use a generic `{medium}` word. Consoles never get
    software-medium flavour at all.
  - **Cross-version claims only when a sibling exists.** Lines like "buy the
    other version" or "the port renders lower" now appear only for titles that
    actually shipped on more than one platform, and name a real sibling.
  - **Old games read as old.** A title reviewed years after release can be
    framed as a retrospective ("revisiting this 17-year-old RPG…"), never for a
    recent one.
  - **More developer / genre / title flavour**, and **sparing, self-censored
    profanity** (`****`) confined to the angry reviewer voices.

  This changes the canonical review/comment **text** and slightly shifts the
  generated **counts** (the text engine consumes randomness differently), so a
  rebuilt database differs from a pre-change one; it remains fully deterministic
  for a given seed. Correctness fixes are English-first; the other language
  banks are unaffected (they carry no medium claims).

### Fixed
- **PostgreSQL warehouse now analyzes itself after the ETL.**
  `batch.usp_refresh_everything` runs `ANALYZE` on every `dw` table at the end of
  the refresh. Postgres doesn't auto-collect statistics on a freshly bulk-loaded
  table the way SQL Server does, so without this the planner never chose the BRIN
  indexes on the date-ordered fact tables and every date-range query
  sequentially scanned the whole fact table. With stats present, a one-month
  `fact_sales` query on the medium tier drops from a full ~29 GB scan to a BRIN
  bitmap scan touching ~230 MB (~99.7% of block ranges pruned).
- HR payroll id determinism: `payroll_run_id` (and the `payroll_lines` foreign
  key) was assigned while iterating a Go map of countries, so the numbering
  varied between builds — non-deterministic run-to-run. Now numbered in a stable
  sorted country order, so payroll ids are reproducible. Also hardened
  `catalog.Platforms()` to sort internally (output-preserving).
- Web address determinism: the standalone `--emit web` path sampled
  pure-population customer cities instead of the era-damped ones the full
  pipeline uses, so review text (and the review/comment/vote counts) differed
  between `--emit web` and `--emit full`. The era-aware shop-proximity weighting
  is now applied in both paths (shared helper, idempotent). `--emit full` output
  is unchanged; standalone `--emit web` now matches it.

### Removed
- The `--vusers` multi-worker option and the parallel transaction-load path. The
  OLTP transaction phase now always loads serially, which makes a build
  unconditionally byte-identical for a given seed — including the surrogate ids,
  and including a row-for-row match across the SQL Server and PostgreSQL
  backends (there is no longer a build-mode that permutes ids). The web
  clickstream is still loaded in parallel across CPU cores; that never affected
  the data.

## [0.1.0] — 2026-08-21

Initial public release (early access — the schema and tiers may still change
before a 1.0).

### Added
- Deterministic (seed 42) synthetic dataset for a fictional 1986–2016 video-game
  retailer, across four size tiers (`tiny` → `large`):
  - **OLTP** (`dbo`, `hr`, `finance`) — shops, catalogue, customers, staff,
    transactions, payments, trade-ins, and finance roll-ups.
  - **Web / community** (`web`) — accounts, reviews, comment threads, votes, and
    a page-view clickstream.
  - **Data warehouse** — dimensional model, line-grain facts, rollups, columnstore.
- One-command build-and-validate pipeline: `build_emporium --emit full`, ending
  in a reconciliation gate that reconciles the warehouse against source
  (`exit 0` on pass).
- Prebuilt SQL Server backups per tier on the Releases page, each with a SHA-256.

### Determinism
- Given the same seed, a rebuild is byte-identical. Surrogate ids
  (`transaction_id`, `page_view_id`, …) are assigned in build order; all
  aggregate/analytical results are identical.
