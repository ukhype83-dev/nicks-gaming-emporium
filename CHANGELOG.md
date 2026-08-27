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

### Fixed
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
- Given the same seed and the same `--vusers`, a rebuild is byte-identical.
  Surrogate ids (`transaction_id`, `page_view_id`, …) are assigned in build
  order, so reproduce with the same `--vusers`; all aggregate/analytical results
  are identical regardless.
