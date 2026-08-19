// Struct → row-slice converters per target entity.
//
// Column ordering is load-bearing: the `Columns` slice returned here
// MUST match the DDL column order in schema_v1_sqlserver.sql for bulk
// copy to work. Any schema change needs a matching edit here.
//
// Organised by schema section (catalog, ops, customers, HR,
// transactions, build metadata) — not alphabetically.
package dbwriter

import (
	"sort"
	"strings"
	"time"

	"emporium/internal/catalog"
	"emporium/internal/customers"
	"emporium/internal/hardware"
	"emporium/internal/hr"
	"emporium/internal/policy"
	"emporium/internal/shops"
	"emporium/internal/transactions"
)

// ---------- helpers ----------

// mustParseDate parses a YYYY-MM-DD string to time.Time at 00:00 UTC.
// Panics on bad input — the simulator always emits valid dates.
func mustParseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic("bad date: " + s + " — " + err.Error())
	}
	return t
}

// mustParseTimestamp parses an RFC-3339 timestamp (used on
// signed_up_at, occurred_at, created_at columns).
func mustParseTimestamp(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// fall back to second-precision format
		t2, err2 := time.Parse("2006-01-02T15:04:05Z", s)
		if err2 != nil {
			panic("bad timestamp: " + s + " — " + err.Error())
		}
		return t2
	}
	return t
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullIfNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullIfNilInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullIfNilDate(p *string) any {
	if p == nil {
		return nil
	}
	return mustParseDate(*p)
}

func nullIfNilTimestamp(p *string) any {
	if p == nil {
		return nil
	}
	return mustParseTimestamp(*p)
}

// ---------- catalog: platforms & releases ----------

var PlatformColumns = []string{
	"platform_id", "name", "family", "released_year", "discontinued_year",
}

// PlatformsFromCatalog builds deterministic platform rows from the
// distinct platform names in the catalog. Family / released_year /
// discontinued_year come from the policy.PlatformLifecycles lookup;
// platforms with no lifecycle entry land with NULLs in those columns
// (and won't match era-aware sampling later).
//
// V1.14.6: IDs assigned in chronological order of console release year
// (ascending). Atari 2600 (1977) is platform_id=1, then Intellivision
// (1979), ColecoVision (1982), NES (1983), SNES (1990), and so on. Ties
// (same release year) and platforms without a lifecycle entry break
// alphabetically. Pre-V1.14.6 IDs were alphabetical — meaning Super
// Nintendo landed at id ~40 despite being one of the earlier consoles.
func PlatformsFromCatalog(cat *catalog.Index) (names []string, rows [][]any, idByName map[string]int) {
	seen := make(map[string]bool)
	for _, r := range cat.All() {
		seen[r.Platform] = true
	}
	names = make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	// V1.14.6: sort by (released_year asc, name asc). Platforms with no
	// lifecycle entry (ReleasedYear == 0) sort to the end via a sentinel
	// rather than colliding with Atari 2600 at slot 1.
	sortYear := func(name string) int {
		y := policy.PlatformLifecycleFor(name).ReleasedYear
		if y == 0 {
			return 9999 // park unknown-era platforms at the tail
		}
		return y
	}
	sort.Slice(names, func(i, j int) bool {
		yi, yj := sortYear(names[i]), sortYear(names[j])
		if yi != yj {
			return yi < yj
		}
		return names[i] < names[j]
	})
	idByName = make(map[string]int, len(names))
	rows = make([][]any, len(names))
	for i, name := range names {
		id := i + 1
		idByName[name] = id
		lc := policy.PlatformLifecycleFor(name)
		var family, released, discontinued any
		if lc.Family != "" {
			family = lc.Family
		}
		if lc.ReleasedYear != 0 {
			released = lc.ReleasedYear
		}
		if lc.DiscontinuedYear != 0 {
			discontinued = lc.DiscontinuedYear
		}
		rows[i] = []any{id, name, family, released, discontinued}
	}
	return
}

var ReleaseColumns = []string{
	"release_id", "title", "normalised_title", "platform_id", "media_type",
	"publisher", "developer", "genre",
	"first_release_date", "first_release_raw",
	"release_jp", "release_na", "release_eu", "release_br",
	"released_jp", "released_na", "released_eu", "released_br",
	// V1.15.3: dropped source_csv, source_url, ingested_at.
	// V1.23.1 (immersion pass): dropped release_other + platform_flags —
	// always-NULL scaffolding columns.
}

// ReleasesToRows converts catalog Releases to dbo.releases rows.
//
// V1.14.2: passes publisher / developer / genre / media_type / regional
// release dates / source_url through from the widened seed TSV. Coverage
// depends on what the SQLite source carries — typically ~96% publisher,
// ~87% developer, ~22% genre, ~33-38% per-region dates pre-Wikidata
// enrichment (V1.14.3 lifts genre and regional dates substantially).
//
// `released_jp/na/eu/br` BIT flags are derived: TRUE iff the
// corresponding date string is non-empty.
func ReleasesToRows(cat *catalog.Index, platformID map[string]int) [][]any {
	releases := cat.All()
	rows := make([][]any, 0, len(releases))
	nullIfEmpty := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	// V1.17.0: normalised_title finally populated. It shipped NULL on
	// every row since the column was added — leaving ix_releases_
	// normalised a fully-built index of pure NULL keys and the one
	// column DESIGNED for searching unusable (training-catalog audit
	// finding F4). Rule: Unicode-lowercase, whitespace collapsed,
	// trimmed, truncated to 450 runes (the NVARCHAR index-key cap the
	// column was sized for).
	normalise := func(title string) any {
		lowered := strings.ToLower(title)
		fields := strings.Fields(lowered)
		if len(fields) == 0 {
			return nil
		}
		n := strings.Join(fields, " ")
		if runes := []rune(n); len(runes) > 450 {
			n = string(runes[:450])
		}
		return n
	}
	// V1.16.0: dates are DATE columns; parse the YYYY-MM-DD strings to
	// time.Time (build_seed_catalog.py guarantees they're already
	// normalised). nullIfEmptyDate returns nil for "" so SQL writes NULL.
	nullIfEmptyDate := func(s string) any {
		if s == "" {
			return nil
		}
		return mustParseDate(s)
	}
	boolBit := func(s string) any {
		if s == "" {
			return nil
		}
		return 1
	}
	for _, r := range releases {
		pid, ok := platformID[r.Platform]
		if !ok {
			continue // skip if platform wasn't in the derived set (shouldn't happen)
		}
		rows = append(rows, []any{
			r.ReleaseID,
			r.Title,
			normalise(r.Title), // V1.17.0 — was nil since the column existed
			pid,
			// V1.23.0: canonical platform-derived medium, replacing the
			// scraped r.MediaType (a Wikidata dump full of howlers — a
			// downloadable N64 Majora's Mask, "vinyl record", NULLs). See
			// policy.CanonicalMedia. Cosmetic to generation; RNG unaffected.
			nullIfEmpty(policy.CanonicalMedia(r.Platform, r.ReleaseDate.Year())),
			nullIfEmpty(r.Publisher),
			nullIfEmpty(r.Developer),
			canonicalGenre(r.Genre), // V1.27: ~2000 raw genres -> ~109 canonical
			r.ReleaseDate, // first_release_date — already time.Time on the Release struct
			nullIfEmpty(r.FirstReleaseRaw),
			nullIfEmptyDate(r.ReleaseJP),
			nullIfEmptyDate(r.ReleaseNA),
			nullIfEmptyDate(r.ReleaseEU),
			nullIfEmptyDate(r.ReleaseBR),
			boolBit(r.ReleaseJP),
			boolBit(r.ReleaseNA),
			boolBit(r.ReleaseEU),
			boolBit(r.ReleaseBR),
		})
	}
	return rows
}

// ---------- retail: shops & shop_addresses ----------

var ShopColumns = []string{
	"shop_id", "shop_code", "name",
	"country_code", "opened_date", "closed_date",
	"currency_code", "source_system",
	"closure_reason", // V1.15.0
}

func ShopsToRows(s []shops.Shop) [][]any {
	rows := make([][]any, len(s))
	for i, shop := range s {
		var closed any
		if shop.ClosedDate != nil {
			closed = mustParseDate(*shop.ClosedDate)
		}
		rows[i] = []any{
			shop.ShopID,
			shop.ShopCode,
			shop.Name,
			shop.CountryCode,
			mustParseDate(shop.OpenedDate),
			closed,
			shop.CurrencyCode,
			shop.SourceSystem,
			nullIfEmpty(shop.ClosureReason), // V1.15.0
		}
	}
	return rows
}

var ShopAddressColumns = []string{
	"shop_address_id", "shop_id",
	"line1", "line2",
	"city", "region", "postal_code", "country_code",
	"latitude", "longitude",
}

func ShopAddressesToRows(s []shops.Shop) [][]any {
	rows := make([][]any, len(s))
	for i, shop := range s {
		a := shop.Address
		rows[i] = []any{
			a.ShopAddressID,
			shop.ShopID,
			a.Line1,
			nil,
			a.City,
			nullIfEmpty(a.Region),
			nullIfEmpty(a.PostalCode),
			a.CountryCode,
			a.Latitude,
			a.Longitude,
		}
	}
	return rows
}

// ---------- retail: customers & customer_addresses ----------

var CustomerColumns = []string{
	"customer_id", "status",
	"signed_up_at", // V1.14.5: signed_up_precision dropped
	"governing_regime",
	"first_name", "last_name", "date_of_birth",
	"source_system", "anonymised_at", "data_retention_expires_at",
}

func CustomerToRow(c customers.Customer) []any {
	return []any{
		c.CustomerID,
		c.Status,
		mustParseTimestamp(c.SignedUpAt),
		c.GoverningRegime,
		nullIfNil(c.FirstName),
		nullIfNil(c.LastName),
		nullIfNilDate(c.DateOfBirth),
		c.SourceSystem,
		nullIfNilTimestamp(c.AnonymisedAt),
		nullIfNilTimestamp(c.DataRetentionExpiresAt),
	}
}

var CustomerAddressColumns = []string{
	"customer_address_id", "customer_id",
	"address_type", "effective_from", "effective_to",
	"line1", "line2",
	"city", "region", "postal_code", "country_code",
	"address_hash", "anonymised_at", "source_system",
}

func CustomerAddressToRow(c customers.Customer) []any {
	a := c.Address
	return []any{
		a.CustomerAddressID,
		c.CustomerID,
		a.AddressType,
		mustParseDate(a.EffectiveFrom),
		nullIfNilDate(a.EffectiveTo),
		a.Line1,
		nullIfEmpty(a.Line2), // V1.27: localised unit line on ~30% of unit-locale rows
		a.City,
		nullIfEmpty(a.Region),
		nullIfEmpty(a.PostalCode),
		a.CountryCode,
		nullIfEmpty(a.AddressHash), // V1.27: a dedup/match key on every row
		nullIfNilTimestamp(a.AnonymisedAt),
		a.SourceSystem,
	}
}

// ---------- hr: reference data (departments, roles, pay_grades) ----------

var DepartmentColumns = []string{"department_id", "code", "name", "parent_department_id"}

func DepartmentsToRows() [][]any {
	rows := make([][]any, len(policy.Departments))
	for i, d := range policy.Departments {
		rows[i] = []any{d.ID, d.Code, d.Name, nil}
	}
	return rows
}

var RoleColumns = []string{"role_id", "code", "name", "is_retail_staff"}

func RolesToRows() [][]any {
	rows := make([][]any, len(policy.Roles))
	for i, r := range policy.Roles {
		rows[i] = []any{r.ID, r.Code, r.Name, r.IsRetailStaff}
	}
	return rows
}

var PayGradeColumns = []string{"pay_grade_id", "code", "description"}

func PayGradesToRows() [][]any {
	rows := make([][]any, len(policy.PayGrades))
	for i, g := range policy.PayGrades {
		rows[i] = []any{g.ID, g.Code, g.Description}
	}
	return rows
}

// ---------- hr: persons, employment_spells, staff_addresses ----------

var PersonColumns = []string{
	"person_id", "first_name", "last_name", "date_of_birth",
	"national_id_hash", "country_of_residence",
	"created_at", "anonymised_at", "source_system",
}

func PersonToRow(p hr.Person) []any {
	return []any{
		p.PersonID,
		p.FirstName,
		p.LastName,
		mustParseDate(p.DateOfBirth),
		nil, // national_id_hash — not generated in V1
		p.CountryOfResidence,
		mustParseTimestamp(p.CreatedAt),
		nil, // anonymised_at
		p.SourceSystem,
	}
}

var EmploymentSpellColumns = []string{
	"spell_id", "person_id",
	"role_id", "department_id", "pay_grade_id", "home_shop_id",
	"started_at", "ended_at", "termination_reason",
	"source_system",
}

func EmploymentSpellToRow(p hr.Person) []any {
	s := p.Spell
	var homeShop any
	if s.HomeShopID != nil {
		homeShop = *s.HomeShopID
	}
	return []any{
		s.SpellID,
		p.PersonID,
		s.RoleID,
		s.DepartmentID,
		s.PayGradeID,
		homeShop,
		mustParseDate(s.StartedAt),
		nullIfNilDate(s.EndedAt),
		nullIfNil(s.TerminationReason),
		s.SourceSystem,
	}
}

var StaffAddressColumns = []string{
	"staff_address_id", "person_id",
	"effective_from", "effective_to",
	"line1", "line2",
	"city", "region", "postal_code", "country_code",
	"anonymised_at",
}

func StaffAddressToRow(p hr.Person) []any {
	a := p.Address
	return []any{
		a.StaffAddressID,
		p.PersonID,
		mustParseDate(a.EffectiveFrom),
		nullIfNilDate(a.EffectiveTo),
		a.Line1,
		nil,
		a.City,
		nullIfEmpty(a.Region),
		nullIfEmpty(a.PostalCode),
		a.CountryCode,
		nullIfNilTimestamp(a.AnonymisedAt),
	}
}

// ---------- retail: transactions, transaction_lines, payments ----------

// ---------- retail: hardware catalog (V1.21.0) ----------

var HardwareColumns = []string{
	"hardware_id", "model_name", "platform_id", "kind", "manufacturer",
	"model_number", "release_na", "release_jp", "release_eu",
	"launch_usd", "revision_of", "notes",
	// V1.23.1 (immersion pass): dropped source_url — Wikipedia URLs in a
	// shop catalog broke the fiction; attribution lives in seed_data/LICENSES.md.
}

// HardwareToRows converts the hardware catalog into dbo.hardware rows,
// resolving platform_id via the same idByName map PlatformsFromCatalog
// returns. Sorted by hardware_id so the revision_of self-FK (which always
// points to a lower id) resolves on insert.
func HardwareToRows(hw *hardware.Index, platformID map[string]int) [][]any {
	models := append([]hardware.Model(nil), hw.All()...)
	sort.Slice(models, func(i, j int) bool { return models[i].HardwareID < models[j].HardwareID })
	dateOrNil := func(t time.Time) any {
		if t.IsZero() {
			return nil
		}
		return t
	}
	strOrNil := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	rows := make([][]any, 0, len(models))
	for _, m := range models {
		pid, ok := platformID[m.Platform]
		if !ok {
			continue // platform absent from the catalog (shouldn't happen)
		}
		var usd any
		if m.LaunchUSD > 0 {
			usd = m.LaunchUSD
		}
		rows = append(rows, []any{
			m.HardwareID, m.ModelName, pid, m.Kind,
			strOrNil(m.Manufacturer), strOrNil(m.ModelNumber),
			dateOrNil(m.ReleaseNA), dateOrNil(m.ReleaseJP), dateOrNil(m.ReleaseEU),
			usd, nullIfZero(m.RevisionOf),
			strOrNil(m.Notes),
		})
	}
	return rows
}

var TransactionColumns = []string{
	"transaction_id", "occurred_at", "occurred_at_precision",
	"shop_id", "channel", "customer_id", "shipping_address_id", "staff_id",
	"till_id", "device_id", "currency_code",
	"subtotal", "tax_total", "discount_total", "total", "trade_in_offset",
	"original_transaction_id",
	"source_system",
}

func TransactionToRow(t transactions.Transaction) []any {
	var customerID any
	if t.CustomerID != nil {
		customerID = *t.CustomerID
	}
	var staffID any
	if t.StaffID != nil {
		staffID = *t.StaffID
	}
	return []any{
		t.TransactionID,
		mustParseTimestamp(t.OccurredAt),
		t.OccurredAtPrecision,
		t.ShopID,
		t.Channel,
		customerID,
		nullIfNilInt(t.ShippingAddressID), // V1.18.0
		staffID,
		nullIfNil(t.TillID),   // V1.18.0
		nullIfNil(t.DeviceID), // V1.18.0
		t.CurrencyCode,
		t.Subtotal,
		t.TaxTotal,
		t.DiscountTotal,
		t.Total,
		t.TradeInOffset,
		nullIfNilInt(t.OriginalTransactionID), // V1.22: refund/return link
		t.SourceSystem,
	}
}

// nullIfZero maps a 0 id to SQL NULL (V1.21: a line/stock/trade-in item is
// software (release_id) XOR hardware (hardware_id); the absent side is 0).
func nullIfZero(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

var TransactionLineColumns = []string{
	"transaction_line_id", "transaction_id",
	"line_number", "release_id", "hardware_id", "condition", "description",
	"quantity", "unit_price", "line_discount", "line_tax", "line_total",
}

func TransactionLinesToRows(t transactions.Transaction) [][]any {
	rows := make([][]any, 0, len(t.Lines))
	for _, l := range t.Lines {
		rows = append(rows, []any{
			l.TransactionLineID,
			t.TransactionID,
			l.LineNumber,
			nullIfZero(l.ReleaseID),  // null on a hardware line
			nullIfZero(l.HardwareID), // V1.21: null on a software line
			l.Condition,
			nil, // description — catalog/hardware-linked, no free text
			l.Quantity,
			l.UnitPrice,
			l.LineDiscount,
			l.LineTax,
			l.LineTotal,
		})
	}
	return rows
}

var PaymentColumns = []string{
	"payment_id", "transaction_id",
	"method", "currency_code", "amount",
	"saved_payment_id", "processor_ref",
}

func PaymentsToRows(t transactions.Transaction) [][]any {
	rows := make([][]any, 0, len(t.Payments))
	for _, p := range t.Payments {
		rows = append(rows, []any{
			p.PaymentID,
			t.TransactionID,
			p.Method,
			p.CurrencyCode,
			p.Amount,
			nullIfNilInt(p.SavedPaymentID),
			nullIfNil(p.ProcessorRef),
		})
	}
	return rows
}

// ---------- retail: inventory + trade-ins (V1.6.2) ----------

var InventoryColumns = []string{
	"inventory_id", "shop_id", "release_id", "hardware_id", "condition",
	"on_hand", "on_order", "reserved", "last_movement_at",
}

func InventoryRowsFromTx(t transactions.Transaction) [][]any {
	rows := make([][]any, 0, len(t.NewInventory))
	for _, r := range t.NewInventory {
		rows = append(rows, []any{
			r.InventoryID, r.ShopID,
			nullIfZero(r.ReleaseID), nullIfZero(r.HardwareID), // V1.21 XOR
			r.Condition,
			r.OnHand, r.OnOrder, r.Reserved,
			nullIfNilTimestamp(r.LastMovementAt),
		})
	}
	return rows
}

var InventoryMovementColumns = []string{
	"movement_id", "inventory_id", "occurred_at", "occurred_at_precision",
	"movement_type", "quantity", "reference_type", "reference_id", "source_system",
}

func InventoryMovementsToRows(t transactions.Transaction) [][]any {
	rows := make([][]any, 0, len(t.Movements))
	for _, m := range t.Movements {
		rows = append(rows, []any{
			m.MovementID,
			m.InventoryID,
			mustParseTimestamp(m.OccurredAt),
			m.OccurredAtPrecision,
			m.MovementType,
			m.Quantity,
			nullIfNil(m.ReferenceType),
			nullIfNilInt(m.ReferenceID),
			m.SourceSystem,
		})
	}
	return rows
}

var TradeInColumns = []string{
	"trade_in_id", "occurred_at", "occurred_at_precision",
	"shop_id", "customer_id", "staff_id", "transaction_id",
	"currency_code", "total_value", "payout_method", "source_system",
}

func TradeInsToRows(t transactions.Transaction) [][]any {
	if t.TradeIn == nil {
		return nil
	}
	ti := t.TradeIn
	return [][]any{{
		ti.TradeInID,
		mustParseTimestamp(ti.OccurredAt),
		ti.OccurredAtPrecision,
		ti.ShopID,
		nullIfNilInt(ti.CustomerID),
		nullIfNilInt(ti.StaffID),
		nullIfNilInt(ti.TransactionID),
		ti.CurrencyCode,
		ti.TotalValue,
		ti.PayoutMethod,
		ti.SourceSystem,
	}}
}

var TradeInItemColumns = []string{
	"trade_in_item_id", "trade_in_id", "release_id", "hardware_id",
	"condition", "valuation", "notes",
}

func TradeInItemsToRows(t transactions.Transaction) [][]any {
	if t.TradeIn == nil {
		return nil
	}
	rows := make([][]any, 0, len(t.TradeIn.Items))
	for _, item := range t.TradeIn.Items {
		rows = append(rows, []any{
			item.TradeInItemID,
			t.TradeIn.TradeInID,
			nullIfZero(item.ReleaseID),
			nullIfZero(item.HardwareID), // V1.21: traded-in console
			item.Condition,
			item.Valuation,
			nullIfNil(item.Notes),
		})
	}
	return rows
}

var StoreCreditLedgerColumns = []string{
	"ledger_id", "customer_id", "occurred_at", "event_type",
	"amount", "currency_code", "trade_in_id", "transaction_id", "source_system",
}

func StoreCreditEventsToRows(t transactions.Transaction) [][]any {
	rows := make([][]any, 0, len(t.StoreCreditEvents))
	for _, e := range t.StoreCreditEvents {
		// V1.18.0: events carry their own CustomerID — the generator
		// only emits grants/uses for identified customers, so the old
		// silent skip-on-NULL is gone. If CustomerID is ever zero here
		// it's a generator bug and the NOT NULL FK will say so loudly.
		rows = append(rows, []any{
			e.LedgerID,
			e.CustomerID,
			mustParseTimestamp(e.OccurredAt),
			e.EventType,
			e.Amount,
			e.CurrencyCode,
			nullIfNilInt(e.TradeInID),
			nullIfNilInt(e.TransactionID),
			e.SourceSystem,
		})
	}
	return rows
}

// ---------- retail: customer engagement (V1.6.0) ----------

var CustomerLifecycleEventColumns = []string{
	"lifecycle_event_id", "customer_id", "event_type", "occurred_at", "reason", "source_system",
}

func CustomerLifecycleEventsToRows(c customers.Customer) [][]any {
	rows := make([][]any, 0, len(c.LifecycleEvents))
	for _, e := range c.LifecycleEvents {
		rows = append(rows, []any{
			e.LifecycleEventID,
			c.CustomerID,
			e.EventType,
			mustParseTimestamp(e.OccurredAt),
			nullIfNil(e.Reason),
			e.SourceSystem,
		})
	}
	return rows
}

var CustomerEmailColumns = []string{
	"customer_email_id", "customer_id", "email", "is_primary", "verified_at", "anonymised_at",
}

func CustomerEmailsToRows(c customers.Customer) [][]any {
	rows := make([][]any, 0, len(c.Emails))
	for _, e := range c.Emails {
		rows = append(rows, []any{
			e.CustomerEmailID,
			c.CustomerID,
			nullIfNil(e.Email),
			e.IsPrimary,
			nullIfNilTimestamp(e.VerifiedAt),
			nullIfNilTimestamp(e.AnonymisedAt),
		})
	}
	return rows
}

var CommunicationPreferenceColumns = []string{
	"comm_pref_id", "customer_id", "channel", "purpose", "opt_in", "updated_at",
}

func CommunicationPreferencesToRows(c customers.Customer) [][]any {
	rows := make([][]any, 0, len(c.CommPrefs))
	for _, p := range c.CommPrefs {
		rows = append(rows, []any{
			p.CommPrefID,
			c.CustomerID,
			p.Channel,
			p.Purpose,
			p.OptIn,
			mustParseTimestamp(p.UpdatedAt),
		})
	}
	return rows
}

var ConsentEventColumns = []string{
	"consent_event_id", "customer_id", "channel", "purpose", "event_type", "occurred_at", "source_system",
}

func ConsentEventsToRows(c customers.Customer) [][]any {
	rows := make([][]any, 0, len(c.ConsentEvents))
	for _, e := range c.ConsentEvents {
		rows = append(rows, []any{
			e.ConsentEventID,
			c.CustomerID,
			e.Channel,
			e.Purpose,
			e.EventType,
			mustParseTimestamp(e.OccurredAt),
			e.SourceSystem,
		})
	}
	return rows
}

var LoyaltyMembershipColumns = []string{
	"membership_id", "customer_id", "scheme", "enrolled_at", "tier", "points_balance", "closed_at",
}

func LoyaltyMembershipsToRows(c customers.Customer) [][]any {
	rows := make([][]any, 0, len(c.LoyaltyMemberships))
	for _, m := range c.LoyaltyMemberships {
		rows = append(rows, []any{
			m.MembershipID,
			c.CustomerID,
			m.Scheme,
			mustParseTimestamp(m.EnrolledAt),
			nullIfNil(m.Tier),
			m.PointsBalance,
			nullIfNilTimestamp(m.ClosedAt),
		})
	}
	return rows
}

var SavedPaymentMethodColumns = []string{
	"saved_payment_id", "customer_id", "method", "token", "card_last4",
	"expiry_month", "expiry_year", "added_at", "removed_at",
}

func SavedPaymentMethodsToRows(c customers.Customer) [][]any {
	rows := make([][]any, 0, len(c.SavedPayments))
	for _, p := range c.SavedPayments {
		var month, year any
		if p.ExpiryMonth != nil {
			month = *p.ExpiryMonth
		}
		if p.ExpiryYear != nil {
			year = *p.ExpiryYear
		}
		rows = append(rows, []any{
			p.SavedPaymentID,
			c.CustomerID,
			p.Method,
			p.Token,
			nullIfNil(p.CardLast4),
			month,
			year,
			mustParseTimestamp(p.AddedAt),
			nullIfNilTimestamp(p.RemovedAt),
		})
	}
	return rows
}

// ---------- hr: contracts, shifts, payroll (V1.6.0) ----------

var ContractColumns = []string{
	"contract_id", "spell_id", "contract_type", "weekly_hours", "signed_at", "source_system",
}

func ContractsToRows(p hr.Person) [][]any {
	rows := make([][]any, 0, len(p.Contracts))
	for _, c := range p.Contracts {
		var hours any
		if c.WeeklyHours != nil {
			hours = *c.WeeklyHours
		}
		rows = append(rows, []any{
			c.ContractID,
			p.Spell.SpellID,
			c.ContractType,
			hours,
			mustParseDate(c.SignedAt),
			c.SourceSystem,
		})
	}
	return rows
}

var StaffShiftColumns = []string{
	"shift_id", "spell_id", "shop_id", "shift_start", "shift_end", "break_minutes", "source_system",
}

func StaffShiftsToRows(p hr.Person) [][]any {
	rows := make([][]any, 0, len(p.Shifts))
	for _, sh := range p.Shifts {
		rows = append(rows, []any{
			sh.ShiftID,
			p.Spell.SpellID,
			sh.ShopID,
			mustParseTimestamp(sh.ShiftStart),
			mustParseTimestamp(sh.ShiftEnd),
			sh.BreakMinutes,
			sh.SourceSystem,
		})
	}
	return rows
}

var PayrollRunColumns = []string{
	"payroll_run_id", "country_code", "period_start", "period_end", "paid_at",
	"currency_code", "status", "source_system",
}

func PayrollRunsToRows(runs []hr.PayrollRun) [][]any {
	rows := make([][]any, 0, len(runs))
	for _, r := range runs {
		rows = append(rows, []any{
			r.PayrollRunID,
			r.CountryCode,
			mustParseDate(r.PeriodStart),
			mustParseDate(r.PeriodEnd),
			mustParseDate(r.PaidAt),
			r.CurrencyCode,
			r.Status,
			r.SourceSystem,
		})
	}
	return rows
}

var PayrollLineColumns = []string{
	"payroll_line_id", "payroll_run_id", "spell_id",
	"gross", "tax", "employee_contribs", "employer_contribs", "net",
}

func PayrollLinesToRows(p hr.Person) [][]any {
	rows := make([][]any, 0, len(p.PayrollLines))
	for _, l := range p.PayrollLines {
		rows = append(rows, []any{
			l.PayrollLineID,
			l.PayrollRunID,
			p.Spell.SpellID,
			l.Gross,
			l.Tax,
			l.EmployeeContribs,
			l.EmployerContribs,
			l.Net,
		})
	}
	return rows
}

// ---------- V1.15.0 — hr.compensation_history ----------

var CompensationHistoryColumns = []string{
	"history_id", "person_id",
	"effective_from", "effective_to",
	"annual_wage", "currency_code",
	"change_reason",
}

func CompensationHistoryToRows(p hr.Person) [][]any {
	if len(p.Compensation) == 0 {
		return nil
	}
	rows := make([][]any, 0, len(p.Compensation))
	for _, c := range p.Compensation {
		rows = append(rows, []any{
			c.HistoryID,
			c.PersonID,
			mustParseDate(c.EffectiveFrom),
			nullIfNilDate(c.EffectiveTo),
			c.AnnualWage,
			c.CurrencyCode,
			c.ChangeReason,
		})
	}
	return rows
}
