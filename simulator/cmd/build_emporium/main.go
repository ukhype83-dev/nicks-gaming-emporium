// build_emporium is the simulator CLI entry point.
//
// Milestone 1 (current): parse CLI, generate deterministic shop records
// + shop addresses per §9.15.4 and §9.12.7. Emit JSON Lines to stdout.
// Milestone 2: DB writer (SQL Server + Postgres).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"emporium/internal/catalog"
	"emporium/internal/hardware"
	"emporium/internal/customers"
	"emporium/internal/dbwriter"
	"emporium/internal/geography"
	"emporium/internal/hr"
	"emporium/internal/shops"
	"emporium/internal/transactions"
)

func main() {
	tier := flag.String("tier", "tiny", "Database tier: tiny | small | medium | large "+
		"(~8 GB / 40 GB / 300 GB / 2.4 TB). Canonical tokens 3g|30g|300g|3t also accepted. §9.9")
	seed := flag.Uint64("seed", 42, "Root seed for deterministic generation")
	asOfStr := flag.String("as-of", "2025-04-23", "Simulated 'now' date, YYYY-MM-DD")
	// Tier accepts the user-facing names tiny/small/medium/large or the
	// canonical tokens 3g/30g/300g/3t (see normalizeTier).
	emit := flag.String("emit", "full", "What to build (default 'full' = everything): full | oltp | shops | customers | hr | transactions | web. "+
		"'full' (alias: 'all') runs the whole pipeline in one command — SQL Server: OLTP → indexes → web → DW schema+procs → ETL → validation; Postgres: OLTP+finance → indexes → web. "+
		"'oltp' builds only the OLTP base (no web/DW). The remaining modes emit a single component (targeted/advanced use).")
	countOnly := flag.Bool("count-only", false, "Don't emit rows to stdout — only print final count on stderr")
	postalPath := flag.String("postal-codes", "../seed_data/postal_codes.tsv",
		"Path to postal_codes.tsv (from build_seed_postal_codes.py)")
	cityPopPath := flag.String("city-populations", "../seed_data/city_populations.tsv",
		"Path to city_populations.tsv (from build_seed_city_populations.py); "+
			"enables population-weighted sampling. Empty string = legacy uniform.")
	cityStreetsPath := flag.String("city-streets", "../seed_data/city_streets.tsv",
		"Path to city_streets.tsv (from build_seed_streets.py); enables "+
			"per-city OSM-sourced real street names. Empty string = legacy generic.")
	genericStreetsPath := flag.String("generic-streets", "../seed_data/generic_streets.tsv",
		"Path to generic_streets.tsv (from build_seed_generic_streets.py); "+
			"OSM-mined cross-city common street names for the long-tail "+
			"fallback. Empty string = legacy 10-name editorial fallback.")
	catalogPath := flag.String("catalog", "../seed_data/releases.tsv",
		"Path to releases.tsv (from build_seed_catalog.py); only needed for --emit transactions")
	hardwarePath := flag.String("hardware", "../seed_data/hardware.tsv",
		"Path to hardware.tsv (console/hardware catalog, V1.21.0); enables hardware sales")
	loadMSSQL := flag.String("load-mssql", "",
		"If set, bulk-insert into SQL Server via this DSN instead of emitting JSON. "+
			"Format: sqlserver://user:pass@host:port?database=emporium.")
	initSchema := flag.Bool("init-schema", false,
		"Apply schema_v1_sqlserver.sql before any load. Requires --load-mssql.")
	schemaFile := flag.String("schema-file", "../schema_v1_sqlserver.sql",
		"Path to schema_v1_sqlserver.sql (tables, PKs, FKs, seed data). "+
			"Used by --init-schema before load.")
	indexesFile := flag.String("indexes-file", "../schema_v1_sqlserver_indexes.sql",
		"Path to schema_v1_sqlserver_indexes.sql (nonclustered indexes). "+
			"Applied AFTER --emit all finishes when --init-schema is set, "+
			"HammerDB-style, to avoid per-row index maintenance during bulk load.")
	recoverySimple := flag.Bool("recovery-simple", false,
		"Set target DB to SIMPLE recovery model before loading. Combined "+
			"with Tablock bulk-copy, gives 10-50× faster bulk loads at "+
			"the cost of no point-in-time restore (fine for benchmark data).")
	vusers := flag.Int("vusers", 1,
		"Number of parallel virtual users (build workers) for the transactions "+
			"phase. >1 spawns N goroutines, each with its own DB connection and "+
			"its own non-overlapping ID range, generating disjoint shop shards "+
			"in parallel. HammerDB-style — biggest single lever for build-phase "+
			"throughput. Trade-off: vusers>1 is NOT resumable (drop+restart on "+
			"failure) and breaks ID monotonicity (IDs are per-worker dense, "+
			"globally sparse). Recommended: 4-16 depending on host CPU count.")
	webBanks := flag.String("web-banks", "../seed_data/web",
		"Directory of V1.26 web phrase banks (--emit web).")
	clickScale := flag.Float64("clickstream-scale", 0,
		"Clickstream volume as a fraction of the 3T daily curve (--emit web). "+
			"0 = auto by tier.")
	deploySQL := flag.String("deploy-sql", "",
		"Apply a GO-separated .sql file to --load-mssql and exit (no emit). "+
			"Used to deploy the reporting/DW schema + stored-procedure library.")
	querySQL := flag.String("query", "",
		"Run a single SQL query against --load-mssql, print rows as TSV, and exit. "+
			"MCP-independent validation path.")
	pgRunETL := flag.Bool("pg-run-etl", false,
		"Run the Postgres DW ETL (CALL batch.usp_refresh_everything(true)) against "+
			"--load-postgres via the autocommit protocol, then exit. Assumes the DW "+
			"schema + batch procedures are already deployed.")
	pgCall := flag.String("pg-call", "",
		"Run one SQL statement against --load-postgres via the autocommit (simple) "+
			"protocol, then exit — e.g. --pg-call \"CALL loadgen.usp_BatchCycle('facts')\". "+
			"Unlike --deploy-sql this does NOT wrap the statement in a transaction, so "+
			"procedures that COMMIT (the batch/loadgen write jobs) work.")
	sqlRoot := flag.String("sql-root", "..",
		"Root directory that contains sql/ and the schema files (--emit full only). "+
			"Default '..' = repo root when run from simulator/. The DW schema, procs "+
			"and the ETL runner are read from <sql-root>/sql/... . (When the SQL is "+
			"eventually embedded into the binary this flag becomes an override, not a "+
			"requirement.)")
	validate := flag.Bool("validate", true,
		"After --emit full, run the reconciliation gate (fact vs OLTP counts/sums, "+
			"USD vs monthly_summary, web contiguity) and print PASS/FAIL. A failed "+
			"gate exits non-zero (3) so a smoke run is self-certifying.")
	loadPostgres := flag.String("load-postgres", "",
		"Second backend (P1): load into PostgreSQL via this DSN instead of SQL Server. "+
			"Format: postgres://user:pass@host:port/dbname . Works with --emit all "+
			"(OLTP, vusers=1), --deploy-sql, and --query.")
	pgSchemaFile := flag.String("pg-schema-file", "../schema_v1_postgres.sql",
		"Postgres schema file applied by --init-schema when --load-postgres is set.")
	pgIndexesFile := flag.String("pg-indexes-file", "../schema_v1_postgres_indexes.sql",
		"Postgres indexes file applied after --emit all when --init-schema + --load-postgres.")
	flag.Parse()
	*tier = normalizeTier(*tier)

	asOf, err := time.Parse("2006-01-02", *asOfStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad --as-of %q: %v\n", *asOfStr, err)
		os.Exit(2)
	}

	// --deploy-sql: apply a .sql file and exit. Standalone from the emit
	// pipeline — no geography load, no generation. Routes to Postgres when
	// --load-postgres is set (no GO-split: pgx runs the whole file).
	if *deploySQL != "" {
		ctx := context.Background()
		fmt.Fprintf(os.Stderr, "Deploying %s ...\n", *deploySQL)
		if *loadPostgres != "" {
			if err := dbwriter.InitSchemaPostgres(ctx, *loadPostgres, *deploySQL); err != nil {
				fmt.Fprintf(os.Stderr, "deploy (postgres) failed: %v\n", err)
				os.Exit(1)
			}
		} else if *loadMSSQL != "" {
			// No timeout — a batch refresh file (e.g. building fact_sales at 3T) can
			// legitimately run for hours. DDL/proc deploys finish in seconds anyway.
			if err := dbwriter.InitSchema(ctx, *loadMSSQL, *deploySQL); err != nil {
				fmt.Fprintf(os.Stderr, "deploy failed: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(os.Stderr, "--deploy-sql requires --load-mssql or --load-postgres\n")
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "Deployed %s OK.\n", *deploySQL)
		return
	}

	// --pg-run-etl: run the DW ETL against an existing Postgres DB and exit.
	if *pgRunETL {
		if *loadPostgres == "" {
			fmt.Fprintf(os.Stderr, "--pg-run-etl requires --load-postgres\n")
			os.Exit(2)
		}
		ctx := context.Background()
		fmt.Fprintf(os.Stderr, "Running PG DW ETL: %s ...\n", pgDWETLCall)
		t0 := time.Now()
		if err := dbwriter.RunETLPostgres(ctx, *loadPostgres, pgDWETLCall); err != nil {
			fmt.Fprintf(os.Stderr, "PG DW ETL failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "PG DW ETL completed in %s.\n", time.Since(t0).Round(time.Second))
		return
	}

	// --pg-call: run one autocommit statement (e.g. a loadgen/batch CALL that
	// COMMITs) against Postgres and exit.
	if *pgCall != "" {
		if *loadPostgres == "" {
			fmt.Fprintf(os.Stderr, "--pg-call requires --load-postgres\n")
			os.Exit(2)
		}
		ctx := context.Background()
		fmt.Fprintf(os.Stderr, "Running (autocommit): %s ...\n", *pgCall)
		t0 := time.Now()
		if err := dbwriter.RunETLPostgres(ctx, *loadPostgres, *pgCall); err != nil {
			fmt.Fprintf(os.Stderr, "pg-call failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Done in %s.\n", time.Since(t0).Round(time.Second))
		return
	}

	// --query: run one query and print rows (TSV). MCP-independent validation.
	if *querySQL != "" {
		ctx := context.Background()
		if *loadPostgres != "" {
			if err := dbwriter.QueryToStdoutPostgres(ctx, *loadPostgres, *querySQL); err != nil {
				fmt.Fprintf(os.Stderr, "query (postgres) failed: %v\n", err)
				os.Exit(1)
			}
			return
		}
		if *loadMSSQL == "" {
			fmt.Fprintf(os.Stderr, "--query requires --load-mssql or --load-postgres\n")
			os.Exit(2)
		}
		if err := dbwriter.QueryToStdout(ctx, *loadMSSQL, *querySQL); err != nil {
			fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle --init-schema and --recovery-simple first — independent
	// of the emit target. SQL-Server-only: the Postgres path applies its
	// own schema inside loadAllPostgres, so skip this block entirely when
	// --load-postgres is set (recovery model has no Postgres equivalent).
	if *loadPostgres == "" && *initSchema && !*recoverySimple {
		fmt.Fprintf(os.Stderr, "WARNING: --init-schema without --recovery-simple — full recovery model "+
			"means bulk loads will be 10× slower. Add --recovery-simple unless you need "+
			"point-in-time restore on this synthetic database.\n")
	}
	if *loadPostgres == "" && (*initSchema || *recoverySimple) {
		if *loadMSSQL == "" {
			fmt.Fprintf(os.Stderr, "--init-schema / --recovery-simple require --load-mssql\n")
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if *initSchema {
			fmt.Fprintf(os.Stderr, "Applying schema from %s...\n", *schemaFile)
			if err := dbwriter.InitSchema(ctx, *loadMSSQL, *schemaFile); err != nil {
				fmt.Fprintf(os.Stderr, "schema init failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "Schema applied OK.\n")
		}
		if *recoverySimple {
			fmt.Fprintf(os.Stderr, "Setting target DB to SIMPLE recovery model...\n")
			if err := dbwriter.SetRecoverySimple(ctx, *loadMSSQL); err != nil {
				fmt.Fprintf(os.Stderr, "set recovery simple failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "Recovery model = SIMPLE.\n")
		}
	}

	postals, err := geography.Load(*postalPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading postal codes: %v\n", err)
		os.Exit(1)
	}
	if *cityPopPath != "" {
		pops, err := geography.LoadCityPopulations(*cityPopPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "loading city populations: %v\n", err)
			os.Exit(1)
		}
		postals.ApplyPopulations(pops)
		nCities := 0
		for _, v := range pops {
			nCities += len(v)
		}
		fmt.Fprintf(os.Stderr, "Population-weighted sampling: loaded %d cities1000 entries across %d name-keys\n", nCities, len(pops))
	}
	if *cityStreetsPath != "" {
		if err := geography.LoadCityStreets(*cityStreetsPath); err != nil {
			fmt.Fprintf(os.Stderr, "loading city streets: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Per-city OSM street names loaded from %s\n", *cityStreetsPath)
	}
	if *genericStreetsPath != "" {
		if err := geography.LoadGenericStreets(*genericStreetsPath); err != nil {
			fmt.Fprintf(os.Stderr, "loading generic streets: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Generic OSM street names loaded from %s\n", *genericStreetsPath)
	}

	switch *emit {
	// "full" (the default) and its back-compat alias "all" both build EVERYTHING
	// in one command. For OLTP-only, use "oltp".
	case "full", "all":
		if *loadPostgres != "" {
			// Postgres full build: schema → OLTP + finance → indexes → web.
			// (No DW/ETL/validation phases — not ported to Postgres yet.)
			if *vusers < 1 {
				fmt.Fprintf(os.Stderr, "--vusers must be >= 1\n")
				os.Exit(2)
			}
			if !*initSchema {
				fmt.Fprintf(os.Stderr, "WARNING: --emit full --load-postgres without --init-schema — assuming "+
					"the schema + indexes are already applied. On an empty DB, add --init-schema.\n")
			}
			loadFullPostgres(*tier, *seed, asOf, postals, *catalogPath, *hardwarePath, *loadPostgres,
				*pgSchemaFile, *pgIndexesFile, *webBanks, *sqlRoot, *initSchema, *validate, *clickScale, *vusers)
			return
		}
		if *loadMSSQL == "" {
			fmt.Fprintf(os.Stderr, "--emit full requires --load-mssql or --load-postgres\n")
			os.Exit(2)
		}
		if *vusers < 1 {
			fmt.Fprintf(os.Stderr, "--vusers must be >= 1\n")
			os.Exit(2)
		}
		if !*initSchema {
			fmt.Fprintf(os.Stderr, "WARNING: --emit full without --init-schema — assuming the schema is "+
				"already applied and nonclustered indexes already built. On an empty DB, add "+
				"--init-schema --recovery-simple.\n")
		}
		loadFullMSSQL(*tier, *seed, asOf, postals, *catalogPath, *hardwarePath, *loadMSSQL,
			*indexesFile, *webBanks, *sqlRoot, *initSchema, *validate, *vusers, *clickScale)
		return
	case "oltp":
		// OLTP base only (no web/DW) — advanced/testing. This was the old
		// "all"; "all" now aliases "full" (everything).
		if *loadPostgres != "" {
			// P1 Postgres path — OLTP base, single writer (vusers=1). The
			// parallel transactions path opens MSSQL writers, so it stays
			// SQL-Server-only until a pgx parallel path lands.
			loadAllPostgres(*tier, *seed, asOf, postals, *catalogPath, *hardwarePath,
				*loadPostgres, *pgSchemaFile, *pgIndexesFile, *initSchema)
			return
		}
		if *loadMSSQL == "" {
			fmt.Fprintf(os.Stderr, "--emit oltp requires --load-mssql or --load-postgres\n")
			os.Exit(2)
		}
		if *vusers < 1 {
			fmt.Fprintf(os.Stderr, "--vusers must be >= 1\n")
			os.Exit(2)
		}
		loadAllMSSQL(*tier, *seed, asOf, postals, *catalogPath, *hardwarePath, *loadMSSQL, *indexesFile, *initSchema, *vusers)
		return
	case "shops":
		if *loadMSSQL != "" {
			loadShopsMSSQL(*tier, *seed, asOf, postals, *loadMSSQL)
		} else {
			emitShops(*tier, *seed, asOf, postals, *countOnly)
		}
	case "customers":
		emitCustomers(*tier, *seed, asOf, postals, *countOnly)
	case "hr":
		emitHR(*tier, *seed, asOf, postals, *countOnly)
	case "transactions":
		emitTransactions(*tier, *seed, asOf, postals, *catalogPath, *hardwarePath, *countOnly)
	case "web":
		if *loadPostgres != "" {
			loadWebPostgres(*tier, *seed, asOf, postals, *catalogPath, *hardwarePath, *loadPostgres, *webBanks, *clickScale, *vusers)
			return
		}
		if *loadMSSQL == "" {
			fmt.Fprintf(os.Stderr, "--emit web requires --load-mssql or --load-postgres (it loads directly into the web.* tables)\n")
			os.Exit(2)
		}
		loadWebMSSQL(*tier, *seed, asOf, postals, *catalogPath, *hardwarePath, *loadMSSQL, *webBanks, *clickScale, *vusers)
	default:
		fmt.Fprintf(os.Stderr, "unknown --emit %q (supports: full [default, alias all] | oltp | shops | customers | hr | transactions | web)\n", *emit)
		os.Exit(2)
	}
}

// fatalf prints to stderr and exits non-zero (1). Convenience for the
// orchestration paths, which have many single-line fatal points.
func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

// dwDDLManifest is the ordered DW schema-DDL deploy sequence (dependency
// order: schemas → dims → fx dim → facts → rollups → columnstore). Paths are
// relative to --sql-root. Explicit, NOT a glob — sql/reporting also holds
// interim/immersion scripts that must never run in a clean build.
var dwDDLManifest = []string{
	"sql/reporting/00_schemas.sql",
	"sql/reporting/10_dimensions.sql",
	"sql/reporting/15_dim_fx.sql",
	"sql/reporting/20_facts.sql",
	"sql/reporting/30_rollups.sql",
	"sql/reporting/40_columnstore.sql",
}

// dwETLRunner runs batch.usp_refresh_everything @full=1 (idempotent).
const dwETLRunner = "sql/reporting/run_refresh_everything.sql"

// dwProcFiles returns the stored-procedure files to deploy, in the known-good
// group order batch_* → rpt_* → loadgen* (alphabetical within each group).
// batch_* must precede the ETL runner (it defines the refresh procs); proc↔
// proc references resolve at run time (deferred name resolution), so cross-
// group order is safe.
func dwProcFiles(sqlRoot string) ([]string, error) {
	var out []string
	for _, pat := range []string{"sql/procs/batch_*.sql", "sql/procs/rpt_*.sql", "sql/procs/loadgen*.sql"} {
		m, err := filepath.Glob(filepath.Join(sqlRoot, pat))
		if err != nil {
			return nil, err
		}
		sort.Strings(m)
		out = append(out, m...)
	}
	return out, nil
}

// pgDWDDLManifest is the PostgreSQL DW schema-DDL deploy sequence (mirrors
// dwDDLManifest). Rowstore + BRIN; 40_wide replaces 40_columnstore (Postgres
// has no columnstore). Explicit order, relative to --sql-root.
var pgDWDDLManifest = []string{
	"sql/reporting_pg/00_schemas.sql",
	"sql/reporting_pg/10_dimensions.sql",
	"sql/reporting_pg/15_dim_fx.sql",
	"sql/reporting_pg/20_facts.sql",
	"sql/reporting_pg/30_rollups.sql",
	"sql/reporting_pg/40_wide.sql",
}

// pgDWETLCall runs the whole PostgreSQL DW ETL (idempotent). Executed via
// RunETLPostgres (simple/autocommit protocol) so the fact/wide procedures can
// COMMIT per month — running it through InitSchemaPostgres would wrap it in an
// implicit transaction and the COMMITs would fail.
const pgDWETLCall = "CALL batch.usp_refresh_everything(true)"

// pgProcFiles returns the PostgreSQL stored-procedure files to deploy, in group
// order batch_* → rpt_* → loadgen* (alphabetical within group), mirroring
// dwProcFiles for the procs_pg tree. (P2b E2: only batch_* exist yet.)
func pgProcFiles(sqlRoot string) ([]string, error) {
	var out []string
	for _, pat := range []string{"sql/procs_pg/batch_*.sql", "sql/procs_pg/rpt_*.sql", "sql/procs_pg/loadgen*.sql"} {
		m, err := filepath.Glob(filepath.Join(sqlRoot, pat))
		if err != nil {
			return nil, err
		}
		sort.Strings(m)
		out = append(out, m...)
	}
	return out, nil
}

// loadFullMSSQL runs the entire pipeline in one command: OLTP base → non-
// clustered indexes → web layer → DW schema+procs → DW ETL → validation gate.
// Each phase reuses the same primitives the individual --emit modes use, so
// there is no new generation logic — this is orchestration. Phases carry their
// own resume behaviour (OLTP/web skip already-loaded tables; the ETL is
// idempotent), so a re-run after a mid-pipeline failure is cheap — EXCEPT the
// transactions phase when vusers>1, which is drop-and-restart (see --vusers).
func loadFullMSSQL(tier string, seed uint64, asOf time.Time, postals *geography.Index,
	catalogPath, hardwarePath, dsn, indexesFile, bankDir, sqlRoot string,
	buildIndexes, validate bool, vusers int, clickScale float64) {

	ctx := context.Background()
	overall := time.Now()

	cat, err := catalog.Load(catalogPath)
	if err != nil {
		fatalf("catalog load failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Loaded %d releases from %s\n", cat.Count(), catalogPath)
	hw, err := hardware.Load(hardwarePath)
	if err != nil {
		fatalf("hardware load failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Loaded %d hardware models from %s\n", hw.Count(), hardwarePath)

	w, err := dbwriter.NewMSSQL(ctx, dsn)
	if err != nil {
		fatalf("SQL Server connect failed: %v", err)
	}
	defer w.Close()

	progress := func(table string, n int64) { fmt.Fprintf(os.Stderr, "  loaded %10d into %s\n", n, table) }

	// Phase 1/6 — OLTP base (dbo/hr/finance).
	fmt.Fprintf(os.Stderr, "\n=== [1/6] OLTP base (tier=%s seed=%d vusers=%d) ===\n", tier, seed, vusers)
	p1 := time.Now()
	oltp, err := dbwriter.LoadAll(ctx, w, dsn, vusers, tier, seed, asOf, postals, cat, hw, progress)
	if err != nil {
		fatalf("OLTP load failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "OLTP done: %d transactions, %d lines, %d customers in %s\n",
		oltp.Transactions, oltp.TransactionLines, oltp.Customers, time.Since(p1).Round(time.Second))

	// Phase 2/6 — nonclustered indexes (HammerDB-style, post-load).
	if buildIndexes {
		fmt.Fprintf(os.Stderr, "\n=== [2/6] Nonclustered indexes (%s) ===\n", indexesFile)
		p2 := time.Now()
		if err := dbwriter.InitSchema(ctx, dsn, indexesFile); err != nil {
			fatalf("index build failed: %v", err)
		}
		fmt.Fprintf(os.Stderr, "Indexes built in %s\n", time.Since(p2).Round(time.Second))
	} else {
		fmt.Fprintf(os.Stderr, "\n=== [2/6] Nonclustered indexes — SKIPPED (no --init-schema) ===\n")
	}

	// Phase 3/6 — web layer (accounts/reviews/comments/votes/page_views).
	if clickScale <= 0 {
		clickScale = clickScaleForTier(tier)
	}
	fmt.Fprintf(os.Stderr, "\n=== [3/6] Web layer (clickstream-scale=%g) ===\n", clickScale)
	p3 := time.Now()
	webS, err := dbwriter.LoadWeb(ctx, w, dsn, tier, seed, asOf, postals, cat, hw, bankDir, clickScale, vusers, progress)
	if err != nil {
		fatalf("web load failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Web done: %d accounts, %d reviews, %d comments, %d votes, %d page_views in %s\n",
		webS.Accounts, webS.Reviews, webS.Comments, webS.Votes, webS.PageViews, time.Since(p3).Round(time.Second))

	// Phase 4/6 — DW schema DDL + stored-procedure library.
	fmt.Fprintf(os.Stderr, "\n=== [4/6] DW schema + stored procedures (sql-root=%s) ===\n", sqlRoot)
	p4 := time.Now()
	for _, rel := range dwDDLManifest {
		path := filepath.Join(sqlRoot, rel)
		if _, err := os.Stat(path); err != nil {
			fatalf("DW DDL not found: %s (check --sql-root): %v", path, err)
		}
		fmt.Fprintf(os.Stderr, "  deploy %s\n", rel)
		if err := dbwriter.InitSchema(ctx, dsn, path); err != nil {
			fatalf("deploy %s failed: %v", rel, err)
		}
	}
	procs, err := dwProcFiles(sqlRoot)
	if err != nil {
		fatalf("glob proc files: %v", err)
	}
	if len(procs) == 0 {
		fatalf("no proc files found under %s — check --sql-root", filepath.Join(sqlRoot, "sql/procs"))
	}
	for _, path := range procs {
		fmt.Fprintf(os.Stderr, "  deploy %s\n", filepath.Base(path))
		if err := dbwriter.InitSchema(ctx, dsn, path); err != nil {
			fatalf("deploy %s failed: %v", filepath.Base(path), err)
		}
	}
	fmt.Fprintf(os.Stderr, "DW objects deployed (%d DDL + %d proc files) in %s\n",
		len(dwDDLManifest), len(procs), time.Since(p4).Round(time.Second))

	// Phase 5/6 — DW ETL (fact/dim/rollup build). The long pole at big tiers.
	fmt.Fprintf(os.Stderr, "\n=== [5/6] DW ETL — batch.usp_refresh_everything @full=1 ===\n")
	p5 := time.Now()
	etlPath := filepath.Join(sqlRoot, dwETLRunner)
	if _, err := os.Stat(etlPath); err != nil {
		fatalf("DW ETL runner not found: %s (check --sql-root): %v", etlPath, err)
	}
	if err := dbwriter.InitSchema(ctx, dsn, etlPath); err != nil {
		fatalf("DW ETL failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "DW ETL completed in %s\n", time.Since(p5).Round(time.Second))

	// Phase 6/6 — reconciliation gate.
	if validate {
		fmt.Fprintf(os.Stderr, "\n=== [6/6] Validation gate ===\n")
		if err := runFullValidation(ctx, dsn); err != nil {
			fmt.Fprintf(os.Stderr, "\n*** FULL BUILD FINISHED WITH VALIDATION FAILURES (tier=%s) after %s ***\n",
				tier, time.Since(overall).Round(time.Second))
			os.Exit(3)
		}
	} else {
		fmt.Fprintf(os.Stderr, "\n=== [6/6] Validation gate — SKIPPED (--validate=false) ===\n")
	}

	fmt.Fprintf(os.Stderr, "\n=== FULL BUILD COMPLETE (tier=%s seed=%d) in %s ===\n",
		tier, seed, time.Since(overall).Round(time.Second))
}

// runFullValidation runs the post-build reconciliation checks. Each check is a
// query returning a single (status, detail) row whose verdict is computed in
// SQL. All checks are tier-agnostic (equalities/relative tolerances that hold
// at any extract profile). Returns an error if any check fails.
func runFullValidation(ctx context.Context, dsn string) error {
	type check struct{ name, query string }
	checks := []check{
		{"fact_sales rowcount", `
			SELECT CASE WHEN a=b THEN 'PASS' ELSE 'FAIL' END AS status,
			       CONCAT('fact_sales=',a,'  transaction_lines=',b,'  diff=',a-b) AS detail
			FROM (SELECT (SELECT COUNT_BIG(*) FROM dw.fact_sales) AS a,
			             (SELECT COUNT_BIG(*) FROM dbo.transaction_lines) AS b) x`},
		{"fact_sales SUM(line_total)", `
			SELECT CASE WHEN a=b THEN 'PASS' ELSE 'FAIL' END AS status,
			       CONCAT('fact=',a,'  oltp=',b,'  diff=',a-b) AS detail
			FROM (SELECT (SELECT SUM(line_total) FROM dw.fact_sales) AS a,
			             (SELECT SUM(line_total) FROM dbo.transaction_lines) AS b) x`},
		{"fact_sales SUM(quantity)", `
			SELECT CASE WHEN a=b THEN 'PASS' ELSE 'FAIL' END AS status,
			       CONCAT('fact_qty=',a,'  oltp_qty=',b,'  diff=',a-b) AS detail
			FROM (SELECT (SELECT SUM(CAST(quantity AS BIGINT)) FROM dw.fact_sales) AS a,
			             (SELECT SUM(CAST(quantity AS BIGINT)) FROM dbo.transaction_lines) AS b) x`},
		{"USD vs monthly_summary", `
			SELECT CASE WHEN b IS NULL OR b=0 THEN 'FAIL'
			            WHEN ABS(a-b)/ABS(b) < 0.0001 THEN 'PASS' ELSE 'FAIL' END AS status,
			       CONCAT('fact_usd=',a,'  summary_usd=',b,'  rel_diff=',
			              CONVERT(VARCHAR(48), ABS(a-b)/NULLIF(ABS(b),0))) AS detail
			FROM (SELECT (SELECT SUM(line_total_usd) FROM dw.fact_sales) AS a,
			             (SELECT SUM(revenue_usd) FROM finance.monthly_summary) AS b) x`},
		{"web.accounts contiguous", webContiguityCheck("web.accounts", "account_id")},
		{"web.reviews contiguous", webContiguityCheck("web.reviews", "review_id")},
		{"web.review_comments contiguous", webContiguityCheck("web.review_comments", "comment_id")},
		{"web.review_votes contiguous", webContiguityCheck("web.review_votes", "vote_id")},
		{"web.page_views populated", `
			SELECT CASE WHEN cnt>0 THEN 'PASS' ELSE 'FAIL' END AS status,
			       CONCAT('count=',cnt,'  (strided worker bands — contiguity N/A)') AS detail
			FROM (SELECT COUNT_BIG(*) AS cnt FROM web.page_views) x`},
	}

	allPass := true
	for _, c := range checks {
		status, detail, err := dbwriter.QueryStatus(ctx, dsn, c.query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [ERR ] %-32s %v\n", c.name, err)
			allPass = false
			continue
		}
		mark := "PASS"
		if !strings.EqualFold(status, "PASS") {
			mark = "FAIL"
			allPass = false
		}
		fmt.Fprintf(os.Stderr, "  [%s] %-32s %s\n", mark, c.name, detail)
	}
	if !allPass {
		return fmt.Errorf("one or more validation checks failed")
	}
	fmt.Fprintf(os.Stderr, "  All checks passed.\n")
	return nil
}

// webContiguityCheck asserts a sequential-id table has ids exactly 1..N with
// no gaps: COUNT == MAX and MIN == 1.
func webContiguityCheck(table, idCol string) string {
	return fmt.Sprintf(`
		SELECT CASE WHEN cnt=mx AND mn=1 THEN 'PASS' ELSE 'FAIL' END AS status,
		       CONCAT('count=',cnt,'  min=',mn,'  max=',mx) AS detail
		FROM (SELECT COUNT_BIG(*) AS cnt, MIN(%s) AS mn, MAX(%s) AS mx FROM %s) x`,
		idCol, idCol, table)
}

// runFullValidationPostgres is the Postgres twin of runFullValidation: the same
// nine reconciliation checks, in Postgres dialect (COUNT instead of COUNT_BIG,
// ::text instead of CONVERT, ::bigint casts). Each query computes its own
// PASS/FAIL verdict; any failure returns an error.
func runFullValidationPostgres(ctx context.Context, dsn string) error {
	type check struct{ name, query string }
	checks := []check{
		{"fact_sales rowcount", `
			SELECT CASE WHEN a=b THEN 'PASS' ELSE 'FAIL' END AS status,
			       CONCAT('fact_sales=',a,'  transaction_lines=',b,'  diff=',a-b) AS detail
			FROM (SELECT (SELECT COUNT(*) FROM dw.fact_sales) AS a,
			             (SELECT COUNT(*) FROM dbo.transaction_lines) AS b) x`},
		{"fact_sales SUM(line_total)", `
			SELECT CASE WHEN a=b THEN 'PASS' ELSE 'FAIL' END AS status,
			       CONCAT('fact=',a,'  oltp=',b,'  diff=',a-b) AS detail
			FROM (SELECT (SELECT SUM(line_total) FROM dw.fact_sales) AS a,
			             (SELECT SUM(line_total) FROM dbo.transaction_lines) AS b) x`},
		{"fact_sales SUM(quantity)", `
			SELECT CASE WHEN a=b THEN 'PASS' ELSE 'FAIL' END AS status,
			       CONCAT('fact_qty=',a,'  oltp_qty=',b,'  diff=',a-b) AS detail
			FROM (SELECT (SELECT SUM(quantity::bigint) FROM dw.fact_sales) AS a,
			             (SELECT SUM(quantity::bigint) FROM dbo.transaction_lines) AS b) x`},
		{"USD vs monthly_summary", `
			SELECT CASE WHEN b IS NULL OR b=0 THEN 'FAIL'
			            WHEN ABS(a-b)/ABS(b) < 0.0001 THEN 'PASS' ELSE 'FAIL' END AS status,
			       CONCAT('fact_usd=',a,'  summary_usd=',b,'  rel_diff=',
			              (ABS(a-b)/NULLIF(ABS(b),0))::text) AS detail
			FROM (SELECT (SELECT SUM(line_total_usd) FROM dw.fact_sales) AS a,
			             (SELECT SUM(revenue_usd) FROM finance.monthly_summary) AS b) x`},
		{"web.accounts contiguous", webContiguityCheckPG("web.accounts", "account_id")},
		{"web.reviews contiguous", webContiguityCheckPG("web.reviews", "review_id")},
		{"web.review_comments contiguous", webContiguityCheckPG("web.review_comments", "comment_id")},
		{"web.review_votes contiguous", webContiguityCheckPG("web.review_votes", "vote_id")},
		{"web.page_views populated", `
			SELECT CASE WHEN cnt>0 THEN 'PASS' ELSE 'FAIL' END AS status,
			       CONCAT('count=',cnt,'  (strided worker bands — contiguity N/A)') AS detail
			FROM (SELECT COUNT(*) AS cnt FROM web.page_views) x`},
	}

	allPass := true
	for _, c := range checks {
		status, detail, err := dbwriter.QueryStatusPostgres(ctx, dsn, c.query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [ERR ] %-32s %v\n", c.name, err)
			allPass = false
			continue
		}
		mark := "PASS"
		if !strings.EqualFold(status, "PASS") {
			mark = "FAIL"
			allPass = false
		}
		fmt.Fprintf(os.Stderr, "  [%s] %-32s %s\n", mark, c.name, detail)
	}
	if !allPass {
		return fmt.Errorf("one or more validation checks failed")
	}
	fmt.Fprintf(os.Stderr, "  All checks passed.\n")
	return nil
}

// webContiguityCheckPG is webContiguityCheck in Postgres dialect (COUNT, not
// COUNT_BIG).
func webContiguityCheckPG(table, idCol string) string {
	return fmt.Sprintf(`
		SELECT CASE WHEN cnt=mx AND mn=1 THEN 'PASS' ELSE 'FAIL' END AS status,
		       CONCAT('count=',cnt,'  min=',mn,'  max=',mx) AS detail
		FROM (SELECT COUNT(*) AS cnt, MIN(%s) AS mn, MAX(%s) AS mx FROM %s) x`,
		idCol, idCol, table)
}

// normalizeTier maps the user-facing tier names to the canonical tokens the
// generator switches on. The public vocabulary is tiny / small / medium /
// large (measured full-build sizes ~8 GB / 40 GB / 300 GB / 2.4 TB); the
// canonical tokens (3g/30g/300g/3t) still work for backward compatibility.
// Case-insensitive; any unrecognised value passes through unchanged.
func normalizeTier(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "tiny":
		return "3g"
	case "small":
		return "30g"
	case "medium":
		return "300g"
	case "large":
		return "3t"
	default:
		return t
	}
}

// clickScaleForTier sizes the clickstream to the tier as a fraction of the
// full 3T daily curve. The ladder is a clean ×10 per tier that mirrors the
// customer-count ratio, so pageviews-per-account stays constant (~23) across
// ALL tiers — including the 3T golden, which was realized at 0.1 (312M
// page_views), NOT the theoretical 1.0 (the disk-driven trim). Anchoring 3t
// at 0.1 here keeps a fresh 3T build consistent with the shipped golden and
// with every smaller tier. (Pre-calibration the ladder was 0.0004/0.004/0.04/
// 1.0, which gave 3g/30g/300g ~4× the golden's per-account clickstream — see
// NGE_TIER_COMPARISON.md, immersion-breaker #4.)
func clickScaleForTier(tier string) float64 {
	switch tier {
	case "3g":
		return 0.0001
	case "30g":
		return 0.001
	case "300g":
		return 0.01
	default: // 3t
		return 0.1
	}
}

func loadWebMSSQL(tier string, seed uint64, asOf time.Time, postals *geography.Index, catalogPath, hardwarePath, dsn, bankDir string, clickScale float64, vusers int) {
	ctx := context.Background()
	if clickScale <= 0 {
		clickScale = clickScaleForTier(tier)
	}

	cat, err := catalog.Load(catalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "catalog load failed: %v\n", err)
		os.Exit(1)
	}
	hw, err := hardware.Load(hardwarePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hardware load failed: %v\n", err)
		os.Exit(1)
	}
	w, err := dbwriter.NewMSSQL(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SQL Server connect failed: %v\n", err)
		os.Exit(1)
	}
	defer w.Close()

	start := time.Now()
	progress := func(table string, n int64) { fmt.Fprintf(os.Stderr, "  loaded %10d into %s\n", n, table) }
	fmt.Fprintf(os.Stderr, "Web layer (tier=%s seed=%d clickstream-scale=%g vusers=%d)...\n", tier, seed, clickScale, vusers)
	stats, err := dbwriter.LoadWeb(ctx, w, dsn, tier, seed, asOf, postals, cat, hw, bankDir, clickScale, vusers, progress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "LoadWeb failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr,
		"\nWeb load done: %d accounts, %d reviews, %d comments, %d votes, %d page_views (tier=%s) in %s\n",
		stats.Accounts, stats.Reviews, stats.Comments, stats.Votes, stats.PageViews,
		tier, time.Since(start).Round(time.Millisecond))
}

// loadWebPostgres is the Postgres twin of loadWebMSSQL — the additive web-layer
// load over an existing OLTP Postgres database. The web.* tables ship in
// schema_v1_postgres.sql (applied during the OLTP --init-schema), so there is
// no schema step here. LoadWeb runs the clickstream workers over this one pgx
// pool (shared, concurrency-safe); the dsn is passed only for signature parity
// and is unused on the Postgres path.
func loadWebPostgres(tier string, seed uint64, asOf time.Time, postals *geography.Index, catalogPath, hardwarePath, dsn, bankDir string, clickScale float64, vusers int) {
	ctx := context.Background()
	if clickScale <= 0 {
		clickScale = clickScaleForTier(tier)
	}

	cat, err := catalog.Load(catalogPath)
	if err != nil {
		fatalf("catalog load failed: %v", err)
	}
	hw, err := hardware.Load(hardwarePath)
	if err != nil {
		fatalf("hardware load failed: %v", err)
	}
	w, err := dbwriter.NewPostgres(ctx, dsn)
	if err != nil {
		fatalf("Postgres connect failed: %v", err)
	}
	defer w.Close()

	start := time.Now()
	progress := func(table string, n int64) { fmt.Fprintf(os.Stderr, "  loaded %10d into %s\n", n, table) }
	fmt.Fprintf(os.Stderr, "Web layer [Postgres] (tier=%s seed=%d clickstream-scale=%g vusers=%d)...\n", tier, seed, clickScale, vusers)
	stats, err := dbwriter.LoadWeb(ctx, w, dsn, tier, seed, asOf, postals, cat, hw, bankDir, clickScale, vusers, progress)
	if err != nil {
		fatalf("LoadWeb failed: %v", err)
	}
	fmt.Fprintf(os.Stderr,
		"\nWeb load done [Postgres]: %d accounts, %d reviews, %d comments, %d votes, %d page_views (tier=%s) in %s\n",
		stats.Accounts, stats.Reviews, stats.Comments, stats.Votes, stats.PageViews,
		tier, time.Since(start).Round(time.Millisecond))
}

// loadFullPostgres runs the whole Postgres pipeline in one command: schema →
// OLTP + finance (LoadAll, serial — the parallel tx path is MSSQL-only) →
// nonclustered indexes → web layer (clickstream parallel over the shared pgx
// pool) → DW schema + procedures → DW ETL → validation gate. It mirrors the SQL
// Server --emit full; the DW is rowstore + BRIN (no columnstore) and the ETL
// runs via an autocommit CALL so its fact/wide procedures COMMIT per month.
// Each phase carries its own resume behaviour (OLTP/web skip already-loaded
// tables; the ETL is idempotent), so a re-run into the same DB is cheap.
func loadFullPostgres(tier string, seed uint64, asOf time.Time, postals *geography.Index,
	catalogPath, hardwarePath, dsn, schemaFile, indexesFile, bankDir, sqlRoot string,
	initSchema, validate bool, clickScale float64, vusers int) {

	ctx := context.Background()
	overall := time.Now()
	if clickScale <= 0 {
		clickScale = clickScaleForTier(tier)
	}

	cat, err := catalog.Load(catalogPath)
	if err != nil {
		fatalf("catalog load failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Loaded %d releases from %s\n", cat.Count(), catalogPath)
	hw, err := hardware.Load(hardwarePath)
	if err != nil {
		fatalf("hardware load failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Loaded %d hardware models from %s\n", hw.Count(), hardwarePath)

	if initSchema {
		fmt.Fprintf(os.Stderr, "Applying Postgres schema from %s ...\n", schemaFile)
		if err := dbwriter.InitSchemaPostgres(ctx, dsn, schemaFile); err != nil {
			fatalf("postgres schema init failed: %v", err)
		}
		fmt.Fprintf(os.Stderr, "Schema applied OK.\n")
	}

	w, err := dbwriter.NewPostgres(ctx, dsn)
	if err != nil {
		fatalf("Postgres connect failed: %v", err)
	}
	defer w.Close()

	progress := func(table string, n int64) { fmt.Fprintf(os.Stderr, "  loaded %10d into %s\n", n, table) }

	// [1/3] OLTP base + finance roll-ups (monthly_summary, customer status).
	oStart := time.Now()
	fmt.Fprintf(os.Stderr, "\n[1/6] OLTP + finance (tier=%s seed=%d)...\n", tier, seed)
	stats, err := dbwriter.LoadAll(ctx, w, "", 1, tier, seed, asOf, postals, cat, hw, progress)
	if err != nil {
		fatalf("Postgres LoadAll failed: %v", err)
	}
	fmt.Fprintf(os.Stderr,
		"OLTP + finance done: %d releases, %d shops, %d persons, %d customers, %d transactions in %s\n",
		stats.Releases, stats.Shops, stats.Persons, stats.Customers, stats.Transactions,
		time.Since(oStart).Round(time.Millisecond))

	// [2/3] Nonclustered indexes (the DB is fully indexed before the web load).
	if initSchema {
		iStart := time.Now()
		fmt.Fprintf(os.Stderr, "\n[2/6] Building Postgres indexes from %s ...\n", indexesFile)
		if err := dbwriter.InitSchemaPostgres(ctx, dsn, indexesFile); err != nil {
			fatalf("postgres index build failed: %v", err)
		}
		fmt.Fprintf(os.Stderr, "Indexes built in %s.\n", time.Since(iStart).Round(time.Millisecond))
	} else {
		fmt.Fprintf(os.Stderr, "\n[2/6] Skipping indexes (--init-schema not set).\n")
	}

	// [3/3] Web layer — accounts/reviews/comments/votes + parallel clickstream.
	wStart := time.Now()
	fmt.Fprintf(os.Stderr, "\n[3/6] Web layer (clickstream-scale=%g vusers=%d)...\n", clickScale, vusers)
	ws, err := dbwriter.LoadWeb(ctx, w, dsn, tier, seed, asOf, postals, cat, hw, bankDir, clickScale, vusers, progress)
	if err != nil {
		fatalf("Postgres LoadWeb failed: %v", err)
	}
	fmt.Fprintf(os.Stderr,
		"Web done: %d accounts, %d reviews, %d comments, %d votes, %d page_views in %s\n",
		ws.Accounts, ws.Reviews, ws.Comments, ws.Votes, ws.PageViews,
		time.Since(wStart).Round(time.Millisecond))

	// [4/6] DW schema DDL + stored-procedure library (procs_pg batch_* for now).
	fmt.Fprintf(os.Stderr, "\n[4/6] DW schema + stored procedures (sql-root=%s) ...\n", sqlRoot)
	p4 := time.Now()
	for _, rel := range pgDWDDLManifest {
		path := filepath.Join(sqlRoot, rel)
		if _, err := os.Stat(path); err != nil {
			fatalf("PG DW DDL not found: %s (check --sql-root): %v", path, err)
		}
		fmt.Fprintf(os.Stderr, "  deploy %s\n", rel)
		if err := dbwriter.InitSchemaPostgres(ctx, dsn, path); err != nil {
			fatalf("deploy %s failed: %v", rel, err)
		}
	}
	procs, err := pgProcFiles(sqlRoot)
	if err != nil {
		fatalf("glob PG proc files: %v", err)
	}
	if len(procs) == 0 {
		fatalf("no PG proc files found under %s — check --sql-root", filepath.Join(sqlRoot, "sql/procs_pg"))
	}
	for _, path := range procs {
		fmt.Fprintf(os.Stderr, "  deploy %s\n", filepath.Base(path))
		if err := dbwriter.InitSchemaPostgres(ctx, dsn, path); err != nil {
			fatalf("deploy %s failed: %v", filepath.Base(path), err)
		}
	}
	fmt.Fprintf(os.Stderr, "DW objects deployed (%d DDL + %d proc files) in %s\n",
		len(pgDWDDLManifest), len(procs), time.Since(p4).Round(time.Second))

	// [5/6] DW ETL — CALL batch.usp_refresh_everything(true) via autocommit so
	// the fact/wide procedures COMMIT month by month.
	fmt.Fprintf(os.Stderr, "\n[5/6] DW ETL — %s ...\n", pgDWETLCall)
	p5 := time.Now()
	if err := dbwriter.RunETLPostgres(ctx, dsn, pgDWETLCall); err != nil {
		fatalf("PG DW ETL failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "DW ETL completed in %s\n", time.Since(p5).Round(time.Second))

	// [6/6] reconciliation gate.
	if validate {
		fmt.Fprintf(os.Stderr, "\n[6/6] Validation gate ...\n")
		if err := runFullValidationPostgres(ctx, dsn); err != nil {
			fmt.Fprintf(os.Stderr, "\n*** FULL POSTGRES BUILD FINISHED WITH VALIDATION FAILURES (tier=%s) after %s ***\n",
				tier, time.Since(overall).Round(time.Second))
			os.Exit(3)
		}
	} else {
		fmt.Fprintf(os.Stderr, "\n[6/6] Validation gate — SKIPPED (--validate=false)\n")
	}

	fmt.Fprintf(os.Stderr, "\nFull Postgres build done (tier=%s seed=%d). Total wall clock: %s\n",
		tier, seed, time.Since(overall).Round(time.Millisecond))
}

// loadAllPostgres is the P1 Postgres OLTP loader — loadAllMSSQL mirrored onto
// a pgx Writer. Single-writer (vusers=1): the parallel transactions path opens
// SQL Server writers, so Postgres runs serially until a pgx parallel path is
// added. Optionally applies the schema before the load and the indexes after
// (HammerDB-style post-load index build).
func loadAllPostgres(tier string, seed uint64, asOf time.Time, postals *geography.Index,
	catalogPath, hardwarePath, dsn, schemaFile, indexesFile string, initSchema bool) {

	ctx := context.Background()
	overall := time.Now()

	cat, err := catalog.Load(catalogPath)
	if err != nil {
		fatalf("catalog load failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Loaded %d releases from %s\n", cat.Count(), catalogPath)
	hw, err := hardware.Load(hardwarePath)
	if err != nil {
		fatalf("hardware load failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Loaded %d hardware models from %s\n", hw.Count(), hardwarePath)

	if initSchema {
		fmt.Fprintf(os.Stderr, "Applying Postgres schema from %s ...\n", schemaFile)
		if err := dbwriter.InitSchemaPostgres(ctx, dsn, schemaFile); err != nil {
			fatalf("postgres schema init failed: %v", err)
		}
		fmt.Fprintf(os.Stderr, "Schema applied OK.\n")
	}

	w, err := dbwriter.NewPostgres(ctx, dsn)
	if err != nil {
		fatalf("Postgres connect failed: %v", err)
	}
	defer w.Close()

	start := time.Now()
	progress := func(table string, n int64) { fmt.Fprintf(os.Stderr, "  loaded %10d into %s\n", n, table) }
	stats, err := dbwriter.LoadAll(ctx, w, "", 1, tier, seed, asOf, postals, cat, hw, progress)
	if err != nil {
		fatalf("Postgres LoadAll failed: %v", err)
	}
	fmt.Fprintf(os.Stderr,
		"\nOLTP load done (Postgres): %d releases, %d shops, %d persons, %d customers, %d transactions (tier=%s seed=%d) in %s\n",
		stats.Releases, stats.Shops, stats.Persons, stats.Customers, stats.Transactions,
		tier, seed, time.Since(start).Round(time.Millisecond))

	if initSchema {
		fmt.Fprintf(os.Stderr, "\nBuilding Postgres indexes from %s ...\n", indexesFile)
		iStart := time.Now()
		if err := dbwriter.InitSchemaPostgres(ctx, dsn, indexesFile); err != nil {
			fatalf("postgres index build failed: %v", err)
		}
		fmt.Fprintf(os.Stderr, "Indexes built in %s.\n", time.Since(iStart).Round(time.Millisecond))
	}
	fmt.Fprintf(os.Stderr, "\nTotal wall clock: %s\n", time.Since(overall).Round(time.Millisecond))
}

func loadAllMSSQL(tier string, seed uint64, asOf time.Time, postals *geography.Index, catalogPath, hardwarePath, dsn, indexesFile string, buildIndexes bool, vusers int) {
	// No timeout — a bulk load can legitimately take hours at larger
	// tiers. User can Ctrl+C.
	ctx := context.Background()

	cat, err := catalog.Load(catalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "catalog load failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Loaded %d releases from %s\n", cat.Count(), catalogPath)

	hw, err := hardware.Load(hardwarePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hardware load failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Loaded %d hardware models from %s\n", hw.Count(), hardwarePath)

	w, err := dbwriter.NewMSSQL(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SQL Server connect failed: %v\n", err)
		os.Exit(1)
	}
	defer w.Close()

	start := time.Now()
	progress := func(table string, n int64) {
		fmt.Fprintf(os.Stderr, "  loaded %10d into %s\n", n, table)
	}
	stats, err := dbwriter.LoadAll(ctx, w, dsn, vusers, tier, seed, asOf, postals, cat, hw, progress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "LoadAll failed: %v\n", err)
		os.Exit(1)
	}
	loadDuration := time.Since(start).Round(time.Millisecond)
	fmt.Fprintf(os.Stderr,
		"\nLoad done: %d platforms, %d releases, %d shops, %d persons, %d customers, %d transactions (tier=%s seed=%d as_of=%s) in %s\n",
		stats.Platforms, stats.Releases, stats.Shops, stats.Persons,
		stats.Customers, stats.Transactions, tier, seed,
		asOf.Format("2006-01-02"), loadDuration)

	// HammerDB-style: build nonclustered indexes only after all data
	// is in. Faster than paying per-row index maintenance during load.
	// Only runs when --init-schema was set — if the user brought their
	// own schema, they're responsible for their own indexes.
	if buildIndexes {
		fmt.Fprintf(os.Stderr, "\nBuilding nonclustered indexes from %s...\n", indexesFile)
		indexStart := time.Now()
		if err := dbwriter.InitSchema(ctx, dsn, indexesFile); err != nil {
			fmt.Fprintf(os.Stderr, "index build failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Indexes built in %s.\n", time.Since(indexStart).Round(time.Millisecond))
	}

	fmt.Fprintf(os.Stderr, "\nTotal wall clock: %s\n", time.Since(start).Round(time.Millisecond))
}

func emitShops(tier string, seed uint64, asOf time.Time, postals *geography.Index, countOnly bool) {
	list, err := shops.Generate(tier, seed, asOf, postals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shop generation failed: %v\n", err)
		os.Exit(1)
	}

	if !countOnly {
		w := bufio.NewWriter(os.Stdout)
		defer w.Flush()
		enc := json.NewEncoder(w)
		for _, s := range list {
			if err := enc.Encode(s); err != nil {
				fmt.Fprintf(os.Stderr, "encode failed: %v\n", err)
				os.Exit(1)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Generated %d shops (tier=%s seed=%d as_of=%s)\n",
		len(list), tier, seed, asOf.Format("2006-01-02"))
}

func emitCustomers(tier string, seed uint64, asOf time.Time, postals *geography.Index, countOnly bool) {
	var w *bufio.Writer
	var enc *json.Encoder
	if !countOnly {
		w = bufio.NewWriter(os.Stdout)
		defer w.Flush()
		enc = json.NewEncoder(w)
	}

	start := time.Now()
	n, _, err := customers.Generate(tier, seed, asOf, postals, func(c customers.Customer) {
		if countOnly {
			return
		}
		if err := enc.Encode(c); err != nil {
			fmt.Fprintf(os.Stderr, "encode failed: %v\n", err)
			os.Exit(1)
		}
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "customer generation failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Generated %d customers (tier=%s seed=%d as_of=%s) in %s\n",
		n, tier, seed, asOf.Format("2006-01-02"), time.Since(start).Round(time.Millisecond))
}

func emitHR(tier string, seed uint64, asOf time.Time, postals *geography.Index, countOnly bool) {
	// HR depends on the shop list. Generate shops silently first —
	// they're a few thousand rows at most, fast and cheap.
	shopList, err := shops.Generate(tier, seed, asOf, postals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shop generation (for HR) failed: %v\n", err)
		os.Exit(1)
	}

	var w *bufio.Writer
	var enc *json.Encoder
	if !countOnly {
		w = bufio.NewWriter(os.Stdout)
		defer w.Flush()
		enc = json.NewEncoder(w)
	}

	start := time.Now()
	n, err := hr.Generate(tier, seed, asOf, postals, shopList, nil, func(p hr.Person) {
		if countOnly {
			return
		}
		if err := enc.Encode(p); err != nil {
			fmt.Fprintf(os.Stderr, "encode failed: %v\n", err)
			os.Exit(1)
		}
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "HR generation failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Generated %d people across %d shops (tier=%s seed=%d as_of=%s) in %s\n",
		n, len(shopList), tier, seed, asOf.Format("2006-01-02"),
		time.Since(start).Round(time.Millisecond))
}

func loadShopsMSSQL(tier string, seed uint64, asOf time.Time, postals *geography.Index, dsn string) {
	shopList, err := shops.Generate(tier, seed, asOf, postals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shop generation failed: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	w, err := dbwriter.NewMSSQL(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SQL Server connect failed: %v\n", err)
		os.Exit(1)
	}
	defer w.Close()

	start := time.Now()

	if err := w.BulkInsert(ctx, "dbo", "shops", dbwriter.ShopColumns, dbwriter.ShopsToRows(shopList)); err != nil {
		fmt.Fprintf(os.Stderr, "bulk-insert dbo.shops failed: %v\n", err)
		os.Exit(1)
	}

	if err := w.BulkInsert(ctx, "dbo", "shop_addresses", dbwriter.ShopAddressColumns, dbwriter.ShopAddressesToRows(shopList)); err != nil {
		fmt.Fprintf(os.Stderr, "bulk-insert dbo.shop_addresses failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr,
		"Loaded %d shops + %d shop_addresses into SQL Server (tier=%s seed=%d as_of=%s) in %s\n",
		len(shopList), len(shopList), tier, seed, asOf.Format("2006-01-02"),
		time.Since(start).Round(time.Millisecond))
}

func emitTransactions(tier string, seed uint64, asOf time.Time, postals *geography.Index, catalogPath, hardwarePath string, countOnly bool) {
	shopList, err := shops.Generate(tier, seed, asOf, postals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shop generation (for transactions) failed: %v\n", err)
		os.Exit(1)
	}
	cat, err := catalog.Load(catalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "catalog load failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Loaded %d releases from %s\n", cat.Count(), catalogPath)
	hw, err := hardware.Load(hardwarePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hardware load failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Loaded %d hardware models from %s\n", hw.Count(), hardwarePath)

	var w *bufio.Writer
	var enc *json.Encoder
	if !countOnly {
		w = bufio.NewWriter(os.Stdout)
		defer w.Flush()
		enc = json.NewEncoder(w)
	}

	// V1.18.0: the standalone path now builds the customer index and
	// the shift rota via quiet generation passes (rows discarded), so
	// emitted transactions carry the full linkage feature set —
	// customer_id, staff_id, store credit, saved payments, shipping.
	// Without these the V1.18 smoke assertions would have nothing to
	// check. Both passes are deterministic and byte-identical to what
	// a DB load would feed the generator.
	_, custIndex, err := customers.Generate(tier, seed, asOf, postals, func(customers.Customer) {})
	if err != nil {
		fmt.Fprintf(os.Stderr, "customer generation (for transactions) failed: %v\n", err)
		os.Exit(1)
	}
	shiftIdx := hr.NewShiftIndex()
	if _, err := hr.Generate(tier, seed, asOf, postals, shopList, nil, func(p hr.Person) {
		shiftIdx.AddPerson(p)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "hr generation (for transactions) failed: %v\n", err)
		os.Exit(1)
	}
	shiftIdx.Finalize()
	fmt.Fprintf(os.Stderr, "Shift index: %d spans for staff assignment\n", shiftIdx.SpanCount())

	start := time.Now()
	n, err := transactions.Generate(tier, seed, asOf, shopList, cat, hw, custIndex, shiftIdx, func(t transactions.Transaction) {
		if countOnly {
			return
		}
		if err := enc.Encode(t); err != nil {
			fmt.Fprintf(os.Stderr, "encode failed: %v\n", err)
			os.Exit(1)
		}
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "transaction generation failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr,
		"Generated %d transactions across %d shops (tier=%s seed=%d as_of=%s) in %s\n",
		n, len(shopList), tier, seed, asOf.Format("2006-01-02"),
		time.Since(start).Round(time.Millisecond))
}
