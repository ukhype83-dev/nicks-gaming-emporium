// End-to-end load orchestration — takes a Writer and generates + loads
// the full database in FK-dependency order.
//
// Dependency order matters: each table's FKs must resolve at insert
// time, so parent rows land first. The order below mirrors the DDL:
//
//   1. hr reference  — departments, roles, pay_grades
//   2. platforms    — derived from catalog
//   3. releases     — references platforms
//   4. shops        — references countries/currencies/source_systems (seeded by DDL)
//   5. shop_addresses — references shops
//   6. persons       — root HR identity
//   7. employment_spells — references persons + shops + roles + departments + pay_grades
//   8. staff_addresses — references persons
//   9. customers    — references privacy_regimes + source_systems (seeded)
//  10. customer_addresses — references customers + countries
//  11. transactions — references shops
//  12. transaction_lines / payments — reference transactions
//
// Resumability — every phase queries MAX(pk) on its tables before
// starting and skips work that has already landed. Multi-table phases
// take MIN(MAX) across siblings as the safe resume point and DELETE
// any partial-batch rows above it before resuming, so a crash mid-flush
// recovers cleanly. See resume.go.
package dbwriter

import (
	"context"
	"fmt"
	"os"
	"time"

	"emporium/internal/catalog"
	"emporium/internal/hardware"
	"emporium/internal/customers"
	"emporium/internal/geography"
	"emporium/internal/hr"
	"emporium/internal/shops"
	"emporium/internal/transactions"
)

// LoadAllStats summarises a successful load.
type LoadAllStats struct {
	Platforms         int64
	Releases          int64
	Hardware          int64
	Departments       int64
	Roles             int64
	PayGrades         int64
	Shops             int64
	ShopAddresses     int64
	Persons           int64
	EmploymentSpells  int64
	StaffAddresses    int64
	Contracts         int64
	StaffShifts       int64
	PayrollRuns       int64
	PayrollLines      int64
	Customers         int64
	CustomerAddresses int64
	LifecycleEvents   int64
	CustomerEmails    int64
	CommPrefs         int64
	ConsentEvents     int64
	LoyaltyMember     int64
	SavedPayments     int64
	Transactions       int64
	TransactionLines   int64
	Payments           int64
	Inventory          int64
	InventoryMovements int64
	TradeIns           int64
	TradeInItems       int64
	StoreCreditLedger  int64

	// V1.15.0
	CompensationHistory int64
	MonthlySummary      int64
}

// firstNonNilErr returns the first non-nil error from its args.
func firstNonNilErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// loadIfEmpty inserts `rows` only if `schema.table` has no data.
// Used for the small fixed-content tables loaded via a single atomic
// BulkInsert — if MAX(pk) > 0 the previous run committed everything.
func loadIfEmpty(ctx context.Context, w Writer, schema, table, idCol string, columns []string, rows [][]any) (bool, error) {
	existing, err := w.MaxBigint(ctx, schema, table, idCol)
	if err != nil {
		return false, err
	}
	if existing > 0 {
		return false, nil // already loaded
	}
	if err := w.BulkInsert(ctx, schema, table, columns, rows); err != nil {
		return false, err
	}
	return true, nil
}

// LoadAll drives the end-to-end build. `w` should be an already-
// connected Writer; the schema must already be applied (use
// InitSchema first if needed). The transactions phase loads serially.
func LoadAll(
	ctx context.Context,
	w Writer,
	tier string,
	seed uint64,
	asOf time.Time,
	postals *geography.Index,
	cat *catalog.Index,
	hw *hardware.Index,
	progress func(string, int64),
) (LoadAllStats, error) {
	var s LoadAllStats
	if progress == nil {
		progress = func(string, int64) {}
	}

	// 1. HR reference data
	if err := loadHRReference(ctx, w, &s, progress); err != nil {
		return s, err
	}

	// 2. Platforms
	_, platformRows, platformID := PlatformsFromCatalog(cat)
	if _, err := loadIfEmpty(ctx, w, "dbo", "platforms", "platform_id", PlatformColumns, platformRows); err != nil {
		return s, fmt.Errorf("load dbo.platforms: %w", err)
	}
	s.Platforms = int64(len(platformRows))
	progress("dbo.platforms", s.Platforms)

	// 3. Releases (~22K rows, single BulkInsert via BatchedLoader,
	//    atomic — MAX > 0 means fully loaded.)
	releaseRows := ReleasesToRows(cat, platformID)
	if len(releaseRows) > 0 {
		existing, err := w.MaxBigint(ctx, "dbo", "releases", "release_id")
		if err != nil {
			return s, fmt.Errorf("load dbo.releases: %w", err)
		}
		if existing == 0 {
			releaseLoader := NewBatchedLoader(ctx, w, "dbo", "releases", ReleaseColumns, 0)
			for _, row := range releaseRows {
				if err := releaseLoader.Add(row); err != nil {
					return s, fmt.Errorf("load dbo.releases: %w", err)
				}
			}
			if err := releaseLoader.Flush(); err != nil {
				return s, fmt.Errorf("load dbo.releases: %w", err)
			}
		}
		s.Releases = int64(len(releaseRows))
		progress("dbo.releases", s.Releases)
	}

	// 3b. Hardware catalog (V1.21.0 — 90 models, single BulkInsert,
	//     loadIfEmpty). nil hw → hardware sales disabled.
	if hw != nil {
		hwRows := HardwareToRows(hw, platformID)
		if _, err := loadIfEmpty(ctx, w, "dbo", "hardware", "hardware_id", HardwareColumns, hwRows); err != nil {
			return s, fmt.Errorf("load dbo.hardware: %w", err)
		}
		s.Hardware = int64(len(hwRows))
		progress("dbo.hardware", s.Hardware)
	}

	// 4-5. Shops + shop_addresses
	shopList, err := shops.Generate(tier, seed, asOf, postals)
	if err != nil {
		return s, fmt.Errorf("shops.Generate: %w", err)
	}
	if err := loadShops(ctx, w, shopList, &s, progress); err != nil {
		return s, err
	}

	// Era-aware shop-proximity weighting (V1.13 / V1.23.1): dampen customer +
	// staff address sampling toward cities near a shop, bucketed by shop-opening
	// era. Runs after shops are placed and before any customer/staff sampling.
	// Shared with LoadWeb so a standalone web load reproduces the identical
	// postals state — see applyShopProximityEras (load_web.go).
	applyShopProximityEras(postals, shopList)

	// 6. Payroll runs — independent (no per-person FKs); precomputed
	//    so HR's per-spell payroll_lines can FK into it.
	payrollRuns, err := loadPayrollRuns(ctx, w, seed, asOf, shopList, &s, progress)
	if err != nil {
		return s, err
	}

	// 7-8. HR people + per-spell ops (contracts/shifts/payroll_lines).
	// V1.18.0: also returns the shift index for staff_id assignment.
	shiftIdx, err := loadHR(ctx, w, tier, seed, asOf, postals, shopList, payrollRuns, &s, progress)
	if err != nil {
		return s, err
	}

	// 9-10. Customers + customer_addresses + engagement collection
	custIndex, err := loadCustomers(ctx, w, tier, seed, asOf, postals, &s, progress)
	if err != nil {
		return s, err
	}

	// 11-12. Transactions + lines + payments (serial).
	if err := loadTransactions(ctx, w, tier, seed, asOf, shopList, cat, hw, custIndex, shiftIdx, &s, progress); err != nil {
		return s, err
	}

	// V1.15.0 — 12.6: finance.monthly_summary (post-load aggregation).
	if err := populateMonthlySummary(ctx, w, &s, progress); err != nil {
		return s, fmt.Errorf("populate finance.monthly_summary: %w", err)
	}

	// V1.27 — 12.7: reclassify dormant customers from transaction recency
	// (post-transaction; leaves everyone 'active' if no transactions loaded).
	if err := populateCustomerStatus(ctx, w, &s, progress); err != nil {
		return s, fmt.Errorf("populate customer status: %w", err)
	}

	return s, nil
}

func loadHRReference(ctx context.Context, w Writer, s *LoadAllStats, progress func(string, int64)) error {
	deptRows := DepartmentsToRows()
	if _, err := loadIfEmpty(ctx, w, "hr", "departments", "department_id", DepartmentColumns, deptRows); err != nil {
		return fmt.Errorf("load hr.departments: %w", err)
	}
	s.Departments = int64(len(deptRows))
	progress("hr.departments", s.Departments)

	roleRows := RolesToRows()
	if _, err := loadIfEmpty(ctx, w, "hr", "roles", "role_id", RoleColumns, roleRows); err != nil {
		return fmt.Errorf("load hr.roles: %w", err)
	}
	s.Roles = int64(len(roleRows))
	progress("hr.roles", s.Roles)

	gradeRows := PayGradesToRows()
	if _, err := loadIfEmpty(ctx, w, "hr", "pay_grades", "pay_grade_id", PayGradeColumns, gradeRows); err != nil {
		return fmt.Errorf("load hr.pay_grades: %w", err)
	}
	s.PayGrades = int64(len(gradeRows))
	progress("hr.pay_grades", s.PayGrades)
	return nil
}

func loadShops(ctx context.Context, w Writer, shopList []shops.Shop, s *LoadAllStats, progress func(string, int64)) error {
	tables := []phaseTable{
		{schema: "dbo", table: "shops", anchor: "shop_id"},
		{schema: "dbo", table: "shop_addresses", anchor: "shop_id"},
	}
	wm, err := resume(ctx, w, "shops", tables)
	if err != nil {
		return err
	}

	shopRows := ShopsToRows(shopList)
	addrRows := ShopAddressesToRows(shopList)

	// Filter to only rows whose shop_id > watermark.
	var newShops, newAddrs [][]any
	for i := range shopList {
		if shopList[i].ShopID > wm {
			newShops = append(newShops, shopRows[i])
			newAddrs = append(newAddrs, addrRows[i])
		}
	}
	if len(newShops) > 0 {
		if err := w.BulkInsert(ctx, "dbo", "shops", ShopColumns, newShops); err != nil {
			return fmt.Errorf("load dbo.shops: %w", err)
		}
		if err := w.BulkInsert(ctx, "dbo", "shop_addresses", ShopAddressColumns, newAddrs); err != nil {
			return fmt.Errorf("load dbo.shop_addresses: %w", err)
		}
	}
	s.Shops = int64(len(shopList))
	s.ShopAddresses = int64(len(shopList))
	progress("dbo.shops", s.Shops)
	progress("dbo.shop_addresses", s.ShopAddresses)
	return nil
}

// loadPayrollRuns generates and inserts hr.payroll_runs in a single
// pass — no per-person dependency, so it runs before HR people. The
// returned slice is passed into loadHR so payroll_lines can FK into it.
func loadPayrollRuns(
	ctx context.Context, w Writer,
	seed uint64, asOf time.Time, shopList []shops.Shop,
	s *LoadAllStats, progress func(string, int64),
) ([]hr.PayrollRun, error) {
	runs := hr.GeneratePayrollRuns(seed, asOf, shopList)
	existing, err := w.MaxBigint(ctx, "hr", "payroll_runs", "payroll_run_id")
	if err != nil {
		return nil, fmt.Errorf("payroll_runs watermark: %w", err)
	}
	if existing == 0 && len(runs) > 0 {
		loader := NewBatchedLoader(ctx, w, "hr", "payroll_runs", PayrollRunColumns, 0)
		for _, row := range PayrollRunsToRows(runs) {
			if err := loader.Add(row); err != nil {
				return nil, fmt.Errorf("load hr.payroll_runs: %w", err)
			}
		}
		if err := loader.Flush(); err != nil {
			return nil, fmt.Errorf("flush hr.payroll_runs: %w", err)
		}
	}
	s.PayrollRuns = int64(len(runs))
	progress("hr.payroll_runs", s.PayrollRuns)
	return runs, nil
}

// loadHR loads the HR domain and (V1.18.0) returns the in-memory
// shift index the transactions phase uses for staff_id assignment.
func loadHR(
	ctx context.Context, w Writer,
	tier string, seed uint64, asOf time.Time,
	postals *geography.Index, shopList []shops.Shop,
	payrollRuns []hr.PayrollRun,
	s *LoadAllStats, progress func(string, int64),
) (*hr.ShiftIndex, error) {
	// HR has two anchor families (person_id and spell_id, 1:1 within
	// a person). Compute watermark across both, take the MIN, and
	// clean up both families above it. The generator skip-threshold
	// is the same value (since person_id == spell_id under our
	// generation scheme).
	personTables := []phaseTable{
		{schema: "hr", table: "persons", anchor: "person_id"},
		{schema: "hr", table: "employment_spells", anchor: "person_id"},
		{schema: "hr", table: "staff_addresses", anchor: "person_id"},
	}
	spellTables := []phaseTable{
		{schema: "hr", table: "contracts", anchor: "spell_id"},
		{schema: "hr", table: "staff_shifts", anchor: "spell_id"},
		{schema: "hr", table: "payroll_lines", anchor: "spell_id"},
	}
	wmPersons, err := resumeWatermark(ctx, w, "hr-people", personTables)
	if err != nil {
		return nil, err
	}
	wmSpells, err := resumeWatermark(ctx, w, "hr-ops", spellTables)
	if err != nil {
		return nil, err
	}
	wm := wmPersons
	if wmSpells < wm {
		wm = wmSpells
	}
	if wm > 0 {
		if err := cleanupAbove(ctx, w, personTables, wm); err != nil {
			return nil, fmt.Errorf("hr-people cleanup above %d: %w", wm, err)
		}
		if err := cleanupAbove(ctx, w, spellTables, wm); err != nil {
			return nil, fmt.Errorf("hr-ops cleanup above %d: %w", wm, err)
		}
		fmt.Fprintf(os.Stderr, "  resume: hr phase will skip ids 1..%d (already loaded)\n", wm)
	}

	// Explicit per-person batching for FK-safe coordinated flushes
	// across all six HR tables. Same rationale as loadCustomers /
	// loadTransactions: per-loader auto-flush can't coordinate FKs
	// when child tables (especially shifts at ~520 rows/spell) fill
	// faster than parents.
	const personBatchSize = 1_000
	s.Persons = wm
	s.EmploymentSpells = wm
	s.StaffAddresses = wm

	personRows := make([][]any, 0, personBatchSize)
	spellRows := make([][]any, 0, personBatchSize)
	addrRows := make([][]any, 0, personBatchSize)
	contractRows := make([][]any, 0, personBatchSize)
	shiftRows := make([][]any, 0, personBatchSize*600)
	payLineRows := make([][]any, 0, personBatchSize*100)
	compRows := make([][]any, 0, personBatchSize*40) // V1.15.0

	flush := func() error {
		if len(personRows) == 0 {
			return nil
		}
		n := len(personRows)
		fmt.Fprintf(os.Stderr, "  → flushing %d persons + spells + addrs + contracts %d + shifts %d + payroll_lines %d (running %d)...\n",
			n, len(contractRows), len(shiftRows), len(payLineRows), s.Persons+int64(n))
		start := time.Now()

		// Parents first: persons → spells → addrs.
		if err := w.BulkCopy(ctx, "hr", "persons", PersonColumns, personRows); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ hr.persons FAILED: %v\n", err)
			return fmt.Errorf("hr.persons: %w", err)
		}
		s.Persons += int64(n)
		if err := w.BulkCopy(ctx, "hr", "employment_spells", EmploymentSpellColumns, spellRows); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ hr.employment_spells FAILED: %v\n", err)
			return fmt.Errorf("hr.employment_spells: %w", err)
		}
		s.EmploymentSpells += int64(n)
		if err := w.BulkCopy(ctx, "hr", "staff_addresses", StaffAddressColumns, addrRows); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ hr.staff_addresses FAILED: %v\n", err)
			return fmt.Errorf("hr.staff_addresses: %w", err)
		}
		s.StaffAddresses += int64(n)
		// Children — all FK to spells (and shop/run): order doesn't
		// matter among them.
		if len(contractRows) > 0 {
			if err := w.BulkCopy(ctx, "hr", "contracts", ContractColumns, contractRows); err != nil {
				fmt.Fprintf(os.Stderr, "  ✗ hr.contracts FAILED: %v\n", err)
				return fmt.Errorf("hr.contracts: %w", err)
			}
			s.Contracts += int64(len(contractRows))
		}
		if len(shiftRows) > 0 {
			if err := w.BulkCopy(ctx, "hr", "staff_shifts", StaffShiftColumns, shiftRows); err != nil {
				fmt.Fprintf(os.Stderr, "  ✗ hr.staff_shifts FAILED: %v\n", err)
				return fmt.Errorf("hr.staff_shifts: %w", err)
			}
			s.StaffShifts += int64(len(shiftRows))
		}
		if len(payLineRows) > 0 {
			if err := w.BulkCopy(ctx, "hr", "payroll_lines", PayrollLineColumns, payLineRows); err != nil {
				fmt.Fprintf(os.Stderr, "  ✗ hr.payroll_lines FAILED: %v\n", err)
				return fmt.Errorf("hr.payroll_lines: %w", err)
			}
			s.PayrollLines += int64(len(payLineRows))
		}
		// V1.15.0 — compensation history (FK: persons).
		if len(compRows) > 0 {
			if err := w.BulkCopy(ctx, "hr", "compensation_history", CompensationHistoryColumns, compRows); err != nil {
				fmt.Fprintf(os.Stderr, "  ✗ hr.compensation_history FAILED: %v\n", err)
				return fmt.Errorf("hr.compensation_history: %w", err)
			}
			s.CompensationHistory += int64(len(compRows))
		}
		fmt.Fprintf(os.Stderr, "    committed in %s\n", time.Since(start).Round(time.Millisecond))
		personRows = personRows[:0]
		spellRows = spellRows[:0]
		addrRows = addrRows[:0]
		contractRows = contractRows[:0]
		shiftRows = shiftRows[:0]
		payLineRows = payLineRows[:0]
		compRows = compRows[:0]
		return nil
	}

	// V1.18.0: collect the shift rota for transactions-phase staff
	// assignment. AddPerson runs BEFORE the resume skip — the index
	// must be complete even when the DB rows are skipped on resume.
	shiftIdx := hr.NewShiftIndex()
	var loadErr error
	_, err = hr.Generate(tier, seed, asOf, postals, shopList, payrollRuns, func(p hr.Person) {
		shiftIdx.AddPerson(p)
		if loadErr != nil {
			return
		}
		if p.PersonID <= wm {
			return
		}
		personRows = append(personRows, PersonToRow(p))
		spellRows = append(spellRows, EmploymentSpellToRow(p))
		addrRows = append(addrRows, StaffAddressToRow(p))
		contractRows = append(contractRows, ContractsToRows(p)...)
		shiftRows = append(shiftRows, StaffShiftsToRows(p)...)
		payLineRows = append(payLineRows, PayrollLinesToRows(p)...)
		compRows = append(compRows, CompensationHistoryToRows(p)...) // V1.15.0
		if len(personRows) >= personBatchSize {
			if err := flush(); err != nil {
				loadErr = err
			}
		}
	})
	if err != nil {
		return nil, fmt.Errorf("hr.Generate: %w", err)
	}
	if loadErr != nil {
		return nil, fmt.Errorf("hr load aborted: %w", loadErr)
	}
	if err := flush(); err != nil {
		return nil, fmt.Errorf("hr final flush: %w", err)
	}
	progress("hr.persons", s.Persons)
	progress("hr.employment_spells", s.EmploymentSpells)
	progress("hr.staff_addresses", s.StaffAddresses)
	progress("hr.contracts", s.Contracts)
	progress("hr.staff_shifts", s.StaffShifts)
	progress("hr.payroll_lines", s.PayrollLines)
	progress("hr.compensation_history", s.CompensationHistory) // V1.15.0
	shiftIdx.Finalize()
	fmt.Fprintf(os.Stderr, "  shift index: %d spans retained for staff_id assignment\n", shiftIdx.SpanCount())
	return shiftIdx, nil
}

func loadCustomers(
	ctx context.Context, w Writer,
	tier string, seed uint64, asOf time.Time,
	postals *geography.Index,
	s *LoadAllStats, progress func(string, int64),
) (*customers.Index, error) {
	// All eight customer-side tables anchor on customer_id. Listed
	// parent-first so cleanupAbove deletes children before parents
	// (FK-safe on resume).
	tables := []phaseTable{
		{schema: "dbo", table: "customers", anchor: "customer_id"},
		{schema: "dbo", table: "customer_addresses", anchor: "customer_id"},
		{schema: "dbo", table: "customer_lifecycle_events", anchor: "customer_id"},
		{schema: "dbo", table: "customer_emails", anchor: "customer_id"},
		{schema: "dbo", table: "communication_preferences", anchor: "customer_id"},
		{schema: "dbo", table: "consent_events", anchor: "customer_id"},
		{schema: "dbo", table: "loyalty_memberships", anchor: "customer_id"},
		{schema: "dbo", table: "saved_payment_methods", anchor: "customer_id"},
	}
	wm, err := resume(ctx, w, "customers", tables)
	if err != nil {
		return nil, err
	}

	// Engagement loader batchSizes are deliberately large — each fires
	// only AFTER the parent customers/customer_addresses loaders have
	// auto-flushed their default 50K batches, so FK targets always
	// exist when child rows commit. Sizes pick the worst-case avg rows
	// per customer × default 50K parent batch × ~5x safety margin.
	// Explicit per-customer batching for FK-safe coordinated flushes.
	// BatchedLoader's per-loader auto-flush can't coordinate across
	// tables — engagement children fill faster than customers and
	// trigger commits while customers buffer still has uncommitted
	// rows their FK targets refer to. Same pattern as loadTransactions.
	const custBatchSize = 25_000
	s.Customers = wm
	s.CustomerAddresses = wm

	custRows := make([][]any, 0, custBatchSize)
	addrRows := make([][]any, 0, custBatchSize)
	lifecycleRows := make([][]any, 0, custBatchSize*2)
	emailRows := make([][]any, 0, custBatchSize)
	commPrefRows := make([][]any, 0, custBatchSize*5)
	consentRows := make([][]any, 0, custBatchSize*5)
	loyaltyRows := make([][]any, 0, custBatchSize*2)
	savedPayRows := make([][]any, 0, custBatchSize)

	flush := func() error {
		if len(custRows) == 0 {
			return nil
		}
		n := len(custRows)
		fmt.Fprintf(os.Stderr, "  → flushing %d customers + %d addrs + lifecycle %d + emails %d + commprefs %d + consent %d + loyalty %d + savedpay %d (running %d)...\n",
			n, len(addrRows), len(lifecycleRows), len(emailRows), len(commPrefRows),
			len(consentRows), len(loyaltyRows), len(savedPayRows), s.Customers+int64(n))
		start := time.Now()

		// Parents first.
		if err := w.BulkCopy(ctx, "dbo", "customers", CustomerColumns, custRows); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ dbo.customers FAILED: %v\n", err)
			return fmt.Errorf("dbo.customers: %w", err)
		}
		s.Customers += int64(n)
		if err := w.BulkCopy(ctx, "dbo", "customer_addresses", CustomerAddressColumns, addrRows); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ dbo.customer_addresses FAILED: %v\n", err)
			return fmt.Errorf("dbo.customer_addresses: %w", err)
		}
		s.CustomerAddresses += int64(len(addrRows))

		// Children — all FK to customers, not to each other; order
		// among them doesn't matter.
		type child struct {
			schema, table string
			cols          []string
			rows          [][]any
			counter       *int64
		}
		children := []child{
			{"dbo", "customer_lifecycle_events", CustomerLifecycleEventColumns, lifecycleRows, &s.LifecycleEvents},
			{"dbo", "customer_emails", CustomerEmailColumns, emailRows, &s.CustomerEmails},
			{"dbo", "communication_preferences", CommunicationPreferenceColumns, commPrefRows, &s.CommPrefs},
			{"dbo", "consent_events", ConsentEventColumns, consentRows, &s.ConsentEvents},
			{"dbo", "loyalty_memberships", LoyaltyMembershipColumns, loyaltyRows, &s.LoyaltyMember},
			{"dbo", "saved_payment_methods", SavedPaymentMethodColumns, savedPayRows, &s.SavedPayments},
		}
		for _, c := range children {
			if len(c.rows) == 0 {
				continue
			}
			if err := w.BulkCopy(ctx, c.schema, c.table, c.cols, c.rows); err != nil {
				fmt.Fprintf(os.Stderr, "  ✗ %s.%s FAILED: %v\n", c.schema, c.table, err)
				return fmt.Errorf("%s.%s: %w", c.schema, c.table, err)
			}
			*c.counter += int64(len(c.rows))
		}
		fmt.Fprintf(os.Stderr, "    committed in %s\n", time.Since(start).Round(time.Millisecond))
		custRows = custRows[:0]
		addrRows = addrRows[:0]
		lifecycleRows = lifecycleRows[:0]
		emailRows = emailRows[:0]
		commPrefRows = commPrefRows[:0]
		consentRows = consentRows[:0]
		loyaltyRows = loyaltyRows[:0]
		savedPayRows = savedPayRows[:0]
		return nil
	}

	var loadErr error
	_, custIndex, err := customers.Generate(tier, seed, asOf, postals, func(c customers.Customer) {
		if loadErr != nil {
			return
		}
		if c.CustomerID <= wm {
			return
		}
		custRows = append(custRows, CustomerToRow(c))
		addrRows = append(addrRows, CustomerAddressToRow(c))
		lifecycleRows = append(lifecycleRows, CustomerLifecycleEventsToRows(c)...)
		emailRows = append(emailRows, CustomerEmailsToRows(c)...)
		commPrefRows = append(commPrefRows, CommunicationPreferencesToRows(c)...)
		consentRows = append(consentRows, ConsentEventsToRows(c)...)
		loyaltyRows = append(loyaltyRows, LoyaltyMembershipsToRows(c)...)
		savedPayRows = append(savedPayRows, SavedPaymentMethodsToRows(c)...)

		if len(custRows) >= custBatchSize {
			if err := flush(); err != nil {
				loadErr = err
			}
		}
	})
	if err != nil {
		return nil, fmt.Errorf("customers.Generate: %w", err)
	}
	if loadErr != nil {
		return nil, fmt.Errorf("customer load aborted: %w", loadErr)
	}
	if err := flush(); err != nil {
		return nil, fmt.Errorf("customer final flush: %w", err)
	}
	progress("dbo.customers", s.Customers)
	progress("dbo.customer_addresses", s.CustomerAddresses)
	progress("dbo.customer_lifecycle_events", s.LifecycleEvents)
	progress("dbo.customer_emails", s.CustomerEmails)
	progress("dbo.communication_preferences", s.CommPrefs)
	progress("dbo.consent_events", s.ConsentEvents)
	progress("dbo.loyalty_memberships", s.LoyaltyMember)
	progress("dbo.saved_payment_methods", s.SavedPayments)
	return custIndex, nil
}

// loadTransactions batches by transaction count (not row count) so that
// the parent `transactions` table is always flushed BEFORE its children
// `transaction_lines` and `payments`. A row-count-per-loader approach
// breaks here because lines fill ~1.5× faster than transactions —
// lines hits the batch threshold first, its flush tries to insert rows
// whose transaction_id FK targets are still uncommitted → FK violation.
//
// Batch size of 20K transactions × ~1.5 lines ≈ 30K lines per flush,
// which keeps each BulkInsert comfortably under inline-SQL size limits
// while respecting FK order.
//
// Resumability: pre-flight queries MAX(transaction_id) on all three
// tables, takes MIN as watermark, deletes any rows above it (children
// first to keep FKs happy), and the generator callback skips already-
// loaded transactions. Restart cost = at most one batch's redo.
func loadTransactions(
	ctx context.Context, w Writer,
	tier string,
	seed uint64, asOf time.Time,
	shopList []shops.Shop, cat *catalog.Index, hw *hardware.Index,
	custIndex *customers.Index,
	shiftIdx *hr.ShiftIndex,
	s *LoadAllStats, progress func(string, int64),
) error {
	const txBatchSize = 20_000

	tables := []phaseTable{
		{schema: "dbo", table: "transactions", anchor: "transaction_id"},
		{schema: "dbo", table: "transaction_lines", anchor: "transaction_id"},
		{schema: "dbo", table: "payments", anchor: "transaction_id"},
		// V1.7 — trade_ins.transaction_id is also FK to transactions.
		// Including it in the resume cleanup keeps trade-in data
		// FK-consistent on retry.
		{schema: "dbo", table: "trade_ins", anchor: "transaction_id"},
	}
	wm, err := resume(ctx, w, "transactions", tables)
	if err != nil {
		return err
	}
	// trade_in_items anchors on trade_in_id (FK to trade_ins). After
	// resume cleaned up trade_ins, sweep orphan items + ledger entries.
	// Inventory + inventory_movements + ledger.transaction_id refs
	// don't fit the watermark pattern cleanly — partial-load resume
	// may leave orphan rows in those tables. For V1.7 the practical
	// recommendation is drop+restart on a mid-load failure.
	if wm > 0 {
		tradeWm, err := w.MaxBigint(ctx, "dbo", "trade_ins", "trade_in_id")
		if err != nil {
			return fmt.Errorf("trade_ins watermark: %w", err)
		}
		if err := w.DeleteAbove(ctx, "dbo", "trade_in_items", "trade_in_id", tradeWm); err != nil {
			return fmt.Errorf("trade_in_items orphan cleanup: %w", err)
		}
		// V1.18.0: sweep the ledger by TRANSACTION id, not trade_in_id —
		// credit_used rows carry trade_in_id NULL and would survive a
		// trade_in_id sweep as orphans. Every V1.18 ledger row (grant
		// and use alike) carries transaction_id.
		if err := w.DeleteAbove(ctx, "dbo", "store_credit_ledger", "transaction_id", wm); err != nil {
			return fmt.Errorf("store_credit_ledger orphan cleanup: %w", err)
		}
	}

	// Seed running totals from the rows already in the DB.
	// transactions and payments are 1:1 with watermark; lines varies.
	// Use MAX(transaction_line_id) and MAX(payment_id) for accurate
	// progress headers — the resume() helper only exposes the
	// transaction_id watermark.
	seedTxTotal := wm
	seedPayTotal := wm
	var seedLineTotal int64
	if wm > 0 {
		n, err := w.MaxBigint(ctx, "dbo", "transaction_lines", "transaction_line_id")
		if err != nil {
			return fmt.Errorf("seed line total: %w", err)
		}
		seedLineTotal = n
	}
	s.Transactions = seedTxTotal
	s.TransactionLines = seedLineTotal
	s.Payments = seedPayTotal

	txRows := make([][]any, 0, txBatchSize)
	lineRows := make([][]any, 0, txBatchSize*2)
	payRows := make([][]any, 0, txBatchSize)
	// V1.6.2: inventory + trade-in collections per batch.
	invRows := make([][]any, 0, txBatchSize/2)
	movRows := make([][]any, 0, txBatchSize*2)
	tradeRows := make([][]any, 0, txBatchSize/10)
	itemRows := make([][]any, 0, txBatchSize/5)
	creditRows := make([][]any, 0, txBatchSize/20)

	flush := func() error {
		if len(txRows) == 0 {
			return nil
		}
		n := len(txRows)
		fmt.Fprintf(os.Stderr, "  → flushing %d tx + %d lines + %d pay + %d inv + %d mov + %d trade + %d items + %d credit (running tx=%d)...\n",
			n, len(lineRows), len(payRows), len(invRows), len(movRows),
			len(tradeRows), len(itemRows), len(creditRows), s.Transactions+int64(n))
		start := time.Now()

		// FK order: transactions → lines/payments → inventory →
		// movements (FK to inv + tx) → trade_ins (FK to tx) →
		// trade_in_items (FK to trade_ins) → store_credit_ledger
		// (FK to trade_ins + tx).
		if err := w.BulkCopy(ctx, "dbo", "transactions", TransactionColumns, txRows); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ dbo.transactions FAILED: %v\n", err)
			return fmt.Errorf("dbo.transactions: %w", err)
		}
		s.Transactions += int64(n)
		if err := w.BulkCopy(ctx, "dbo", "transaction_lines", TransactionLineColumns, lineRows); err != nil {
			return fmt.Errorf("dbo.transaction_lines: %w", err)
		}
		s.TransactionLines += int64(len(lineRows))
		if err := w.BulkCopy(ctx, "dbo", "payments", PaymentColumns, payRows); err != nil {
			return fmt.Errorf("dbo.payments: %w", err)
		}
		s.Payments += int64(len(payRows))
		if len(invRows) > 0 {
			if err := w.BulkCopy(ctx, "dbo", "inventory", InventoryColumns, invRows); err != nil {
				return fmt.Errorf("dbo.inventory: %w", err)
			}
			s.Inventory += int64(len(invRows))
		}
		if len(movRows) > 0 {
			if err := w.BulkCopy(ctx, "dbo", "inventory_movements", InventoryMovementColumns, movRows); err != nil {
				return fmt.Errorf("dbo.inventory_movements: %w", err)
			}
			s.InventoryMovements += int64(len(movRows))
		}
		if len(tradeRows) > 0 {
			if err := w.BulkCopy(ctx, "dbo", "trade_ins", TradeInColumns, tradeRows); err != nil {
				return fmt.Errorf("dbo.trade_ins: %w", err)
			}
			s.TradeIns += int64(len(tradeRows))
		}
		if len(itemRows) > 0 {
			if err := w.BulkCopy(ctx, "dbo", "trade_in_items", TradeInItemColumns, itemRows); err != nil {
				return fmt.Errorf("dbo.trade_in_items: %w", err)
			}
			s.TradeInItems += int64(len(itemRows))
		}
		if len(creditRows) > 0 {
			if err := w.BulkCopy(ctx, "dbo", "store_credit_ledger", StoreCreditLedgerColumns, creditRows); err != nil {
				return fmt.Errorf("dbo.store_credit_ledger: %w", err)
			}
			s.StoreCreditLedger += int64(len(creditRows))
		}

		fmt.Fprintf(os.Stderr, "    committed in %s\n", time.Since(start).Round(time.Millisecond))
		txRows = txRows[:0]
		lineRows = lineRows[:0]
		payRows = payRows[:0]
		invRows = invRows[:0]
		movRows = movRows[:0]
		tradeRows = tradeRows[:0]
		itemRows = itemRows[:0]
		creditRows = creditRows[:0]
		return nil
	}

	var loadErr error
	_, err = transactions.Generate(tier, seed, asOf, shopList, cat, hw, custIndex, shiftIdx, func(t transactions.Transaction) {
		if loadErr != nil {
			return
		}
		if t.TransactionID <= wm {
			return
		}
		txRows = append(txRows, TransactionToRow(t))
		for _, lineRow := range TransactionLinesToRows(t) {
			lineRows = append(lineRows, lineRow)
		}
		for _, payRow := range PaymentsToRows(t) {
			payRows = append(payRows, payRow)
		}
		invRows = append(invRows, InventoryRowsFromTx(t)...)
		movRows = append(movRows, InventoryMovementsToRows(t)...)
		tradeRows = append(tradeRows, TradeInsToRows(t)...)
		itemRows = append(itemRows, TradeInItemsToRows(t)...)
		creditRows = append(creditRows, StoreCreditEventsToRows(t)...)
		if len(txRows) >= txBatchSize {
			if err := flush(); err != nil {
				loadErr = err
			}
		}
	})
	if err != nil {
		return fmt.Errorf("transactions.Generate: %w", err)
	}
	if loadErr != nil {
		return fmt.Errorf("transaction load aborted: %w", loadErr)
	}
	if err := flush(); err != nil {
		return fmt.Errorf("transaction final flush: %w", err)
	}

	progress("dbo.transactions", s.Transactions)
	progress("dbo.transaction_lines", s.TransactionLines)
	progress("dbo.payments", s.Payments)
	progress("dbo.inventory", s.Inventory)
	progress("dbo.inventory_movements", s.InventoryMovements)
	progress("dbo.trade_ins", s.TradeIns)
	progress("dbo.trade_in_items", s.TradeInItems)
	progress("dbo.store_credit_ledger", s.StoreCreditLedger)
	return nil
}
