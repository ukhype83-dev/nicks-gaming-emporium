// build_emporium is the simulator CLI entry point: it generates the
// deterministic dataset and loads it into a SQL Server target.
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
	emit := flag.String("emit", "shops", "Entity to emit: full | all | shops | customers | hr | transactions | web. "+
		"'full' runs the whole pipeline in one command: OLTP → indexes → web → DW schema+procs → ETL → validation.")
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
	flag.Parse()
	*tier = normalizeTier(*tier)

	asOf, err := time.Parse("2006-01-02", *asOfStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad --as-of %q: %v\n", *asOfStr, err)
		os.Exit(2)
	}

	// --deploy-sql: apply a GO-separated .sql file (schema/procs) and exit.
	// Standalone from the emit pipeline — no geography load, no generation.
	if *deploySQL != "" {
		if *loadMSSQL == "" {
			fmt.Fprintf(os.Stderr, "--deploy-sql requires --load-mssql\n")
			os.Exit(2)
		}
		// No timeout — a batch refresh file (e.g. building fact_sales at 3T) can
		// legitimately run for hours. DDL/proc deploys finish in seconds anyway.
		ctx := context.Background()
		fmt.Fprintf(os.Stderr, "Deploying %s ...\n", *deploySQL)
		if err := dbwriter.InitSchema(ctx, *loadMSSQL, *deploySQL); err != nil {
			fmt.Fprintf(os.Stderr, "deploy failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Deployed %s OK.\n", *deploySQL)
		return
	}

	// --query: run one query and print rows (TSV). MCP-independent validation.
	if *querySQL != "" {
		if *loadMSSQL == "" {
			fmt.Fprintf(os.Stderr, "--query requires --load-mssql\n")
			os.Exit(2)
		}
		ctx := context.Background()
		if err := dbwriter.QueryToStdout(ctx, *loadMSSQL, *querySQL); err != nil {
			fmt.Fprintf(os.Stderr, "query failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle --init-schema and --recovery-simple first — independent
	// of the emit target.
	if *initSchema && !*recoverySimple {
		fmt.Fprintf(os.Stderr, "WARNING: --init-schema without --recovery-simple — full recovery model "+
			"means bulk loads will be 10× slower. Add --recovery-simple unless you need "+
			"point-in-time restore on this synthetic database.\n")
	}
	if *initSchema || *recoverySimple {
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
	case "full":
		if *loadMSSQL == "" {
			fmt.Fprintf(os.Stderr, "--emit full requires --load-mssql\n")
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
	case "all":
		if *loadMSSQL == "" {
			fmt.Fprintf(os.Stderr, "--emit all requires --load-mssql\n")
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
		if *loadMSSQL == "" {
			fmt.Fprintf(os.Stderr, "--emit web requires --load-mssql (it loads directly into the web.* tables)\n")
			os.Exit(2)
		}
		loadWebMSSQL(*tier, *seed, asOf, postals, *catalogPath, *hardwarePath, *loadMSSQL, *webBanks, *clickScale, *vusers)
	default:
		fmt.Fprintf(os.Stderr, "unknown --emit %q (supports: full | all | shops | customers | hr | transactions | web)\n", *emit)
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
