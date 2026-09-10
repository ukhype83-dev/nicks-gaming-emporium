-- Nick's Gaming Emporium — V1 schema (PostgreSQL 15+ port of schema_v1_sqlserver.sql)
/* =============================================================
   Nick's Gaming Emporium — V1 nonclustered indexes (PostgreSQL 15+ port
   of schema_v1_sqlserver_indexes.sql)

   Applied AFTER data load (HammerDB-style) so the bulk insert path
   isn't paying per-row index maintenance cost.

   Pairs with schema_v1_postgres.sql (tables/PKs/FKs/seed).

   Porting notes:
     - SQL Server "filtered indexes" (WHERE ...) become Postgres
       "partial indexes" — identical WHERE syntax, with one exception:
       `WHERE is_primary = 1` becomes `WHERE is_primary` (the column is
       now BOOLEAN, and Postgres has no implicit int->bool cast).
     - GO batch markers removed.
     - The trailing T-SQL "re-trust constraints" block is SQL-Server-only
       (sys.check_constraints / sp_executesql). Postgres validates
       constraints at creation and always trusts them, so it is dropped.
   ============================================================= */

/* public.releases */
CREATE INDEX ix_releases_platform        ON public.releases(platform_id);
CREATE INDEX ix_releases_normalised      ON public.releases(normalised_title);
CREATE INDEX ix_releases_first_release   ON public.releases(first_release_date);

/* public.hardware — V1.21.0 */
CREATE INDEX ix_hardware_platform ON public.hardware(platform_id);
CREATE INDEX ix_hardware_revision ON public.hardware(revision_of);

/* public.shop_addresses */
CREATE UNIQUE INDEX ux_shop_addresses_one_per_shop ON public.shop_addresses(shop_id);

/* public.inventory — split into two partial uniques (software XOR hardware). */
CREATE UNIQUE INDEX ux_inventory_shop_release_cond
    ON public.inventory(shop_id, release_id, condition)
    WHERE release_id IS NOT NULL;
CREATE UNIQUE INDEX ux_inventory_shop_hardware_cond
    ON public.inventory(shop_id, hardware_id, condition)
    WHERE hardware_id IS NOT NULL;

/* public.inventory_movements */
CREATE INDEX ix_inv_mov_occurred  ON public.inventory_movements(occurred_at);
CREATE INDEX ix_inv_mov_inventory ON public.inventory_movements(inventory_id, occurred_at);

/* public.customers */
CREATE INDEX ix_customers_regime_status ON public.customers(governing_regime, status);
CREATE INDEX ix_customers_signed_up     ON public.customers(signed_up_at);

/* public.customer_addresses — partial unique on the currently-active row. */
CREATE INDEX ix_cust_addr_customer ON public.customer_addresses(customer_id);
CREATE INDEX ix_cust_addr_hash     ON public.customer_addresses(address_hash);
CREATE UNIQUE INDEX ux_cust_addr_current
    ON public.customer_addresses(customer_id, address_type)
    WHERE effective_to IS NULL;

/* public.customer_emails */
CREATE INDEX ix_cust_emails_customer ON public.customer_emails(customer_id);

/* public.communication_preferences */
CREATE UNIQUE INDEX ux_comm_pref_cust_chan_purpose
    ON public.communication_preferences(customer_id, channel, purpose);

/* public.consent_events */
CREATE INDEX ix_consent_events_cust_time
    ON public.consent_events(customer_id, occurred_at);

/* public.customer_lifecycle_events */
CREATE INDEX ix_lifecycle_cust_time ON public.customer_lifecycle_events(customer_id, occurred_at);
CREATE INDEX ix_lifecycle_type_time ON public.customer_lifecycle_events(event_type, occurred_at);

/* public.loyalty_memberships */
CREATE INDEX ix_loyalty_customer ON public.loyalty_memberships(customer_id);

/* public.saved_payment_methods */
CREATE INDEX ix_saved_pay_customer ON public.saved_payment_methods(customer_id);

/* public.transactions
   ix_tx_customer_time is partial: customer_id is NULL on the
   anonymous-walk-in share of sales. */
CREATE INDEX ix_tx_occurred       ON public.transactions(occurred_at);
CREATE INDEX ix_tx_shop_time      ON public.transactions(shop_id, occurred_at);
CREATE INDEX ix_tx_customer_time  ON public.transactions(customer_id, occurred_at)
    WHERE customer_id IS NOT NULL;
CREATE INDEX ix_tx_channel_time   ON public.transactions(channel, occurred_at);
CREATE INDEX ix_tx_source         ON public.transactions(source_system, occurred_at);
/* Refund/return rows link to their original sale — partial. */
CREATE INDEX ix_tx_original       ON public.transactions(original_transaction_id)
    WHERE original_transaction_id IS NOT NULL;

/* public.transaction_lines */
CREATE INDEX ix_txl_transaction ON public.transaction_lines(transaction_id);
CREATE INDEX ix_txl_release     ON public.transaction_lines(release_id);
CREATE INDEX ix_txl_hardware    ON public.transaction_lines(hardware_id)
    WHERE hardware_id IS NOT NULL;

/* public.payments */
CREATE INDEX ix_pay_transaction ON public.payments(transaction_id);

/* public.trade_ins */
CREATE INDEX ix_trade_ins_occurred ON public.trade_ins(occurred_at);
CREATE INDEX ix_trade_ins_customer ON public.trade_ins(customer_id, occurred_at)
    WHERE customer_id IS NOT NULL;

/* public.trade_in_items */
CREATE INDEX ix_trade_in_items_trade_in ON public.trade_in_items(trade_in_id);
CREATE INDEX ix_trade_in_items_hardware ON public.trade_in_items(hardware_id)
    WHERE hardware_id IS NOT NULL;

/* public.store_credit_ledger */
CREATE INDEX ix_store_credit_customer ON public.store_credit_ledger(customer_id, occurred_at);

/* hr.employment_spells */
CREATE INDEX ix_spells_person     ON hr.employment_spells(person_id);
CREATE INDEX ix_spells_active     ON hr.employment_spells(started_at, ended_at);
CREATE INDEX ix_spells_shop       ON hr.employment_spells(home_shop_id);

/* hr.contracts */
CREATE INDEX ix_contracts_spell ON hr.contracts(spell_id);

/* hr.staff_addresses */
CREATE INDEX ix_staff_addr_person ON hr.staff_addresses(person_id);

/* hr.staff_shifts */
CREATE INDEX ix_shifts_shop_time  ON hr.staff_shifts(shop_id, shift_start);
CREATE INDEX ix_shifts_spell_time ON hr.staff_shifts(spell_id, shift_start);

/* hr.payroll_runs */
CREATE INDEX ix_payroll_runs_period ON hr.payroll_runs(country_code, period_end);

/* hr.payroll_lines */
CREATE INDEX ix_payroll_lines_spell ON hr.payroll_lines(spell_id);
CREATE INDEX ix_payroll_lines_run   ON hr.payroll_lines(payroll_run_id);

/* =============================================================
   V1.9.1 — FK-supporting indexes not already covered above
   ============================================================= */

/* Partial unique: at most one is_primary row per customer. */
CREATE UNIQUE INDEX ux_customer_emails_primary
    ON public.customer_emails(customer_id)
    WHERE is_primary;

/* public.transactions — staff + shipping_address FKs (partial). */
CREATE INDEX ix_tx_staff               ON public.transactions(staff_id)
    WHERE staff_id IS NOT NULL;
CREATE INDEX ix_tx_shipping_addr       ON public.transactions(shipping_address_id)
    WHERE shipping_address_id IS NOT NULL;

/* public.payments — saved_payment FK (partial). */
CREATE INDEX ix_payments_saved         ON public.payments(saved_payment_id)
    WHERE saved_payment_id IS NOT NULL;

/* public.trade_ins — shop / linked-transaction / staff FKs. */
CREATE INDEX ix_trade_ins_shop         ON public.trade_ins(shop_id);
CREATE INDEX ix_trade_ins_tx           ON public.trade_ins(transaction_id);
CREATE INDEX ix_trade_ins_staff        ON public.trade_ins(staff_id)
    WHERE staff_id IS NOT NULL;

/* public.trade_in_items — release FK */
CREATE INDEX ix_trade_in_items_release ON public.trade_in_items(release_id);

/* hr.employment_spells — role / dept / pay_grade FKs */
CREATE INDEX ix_emp_spells_role        ON hr.employment_spells(role_id);
CREATE INDEX ix_emp_spells_dept        ON hr.employment_spells(department_id);
CREATE INDEX ix_emp_spells_pay_grade   ON hr.employment_spells(pay_grade_id);

/* V1.15.0 — closure-era tables */
CREATE INDEX ix_shops_closed_date      ON public.shops(closed_date) WHERE closed_date IS NOT NULL;
CREATE INDEX ix_emp_spells_ended       ON hr.employment_spells(ended_at) WHERE ended_at IS NOT NULL;
CREATE INDEX ix_comp_history_person    ON hr.compensation_history(person_id, effective_from);
CREATE INDEX ix_comp_history_reason    ON hr.compensation_history(change_reason);

/* =============================================================
   V1.26 — web (community) layer
   ============================================================= */
CREATE INDEX ix_reviews_release        ON web.reviews(release_id, posted_at) WHERE release_id IS NOT NULL;
CREATE INDEX ix_reviews_hardware       ON web.reviews(hardware_id, posted_at) WHERE hardware_id IS NOT NULL;
CREATE INDEX ix_reviews_account        ON web.reviews(account_id, posted_at);
CREATE INDEX ix_reviews_posted         ON web.reviews(posted_at);

CREATE INDEX ix_comments_review        ON web.review_comments(review_id, posted_at);
CREATE INDEX ix_comments_account       ON web.review_comments(account_id, posted_at);

CREATE INDEX ix_votes_review           ON web.review_votes(review_id);

CREATE INDEX ix_pv_occurred            ON web.page_views(occurred_at);
CREATE INDEX ix_pv_session             ON web.page_views(session_id, occurred_at);
CREATE INDEX ix_pv_path                ON web.page_views(url_path, occurred_at);
CREATE INDEX ix_pv_account             ON web.page_views(account_id, occurred_at) WHERE account_id IS NOT NULL;

/* =============================================================
   V1.9.2 — Re-trust constraints (SQL Server only)
   -------------------------------------------------------------
   The source's trailing T-SQL block walked sys.check_constraints and
   sys.foreign_keys to flip NOT TRUSTED constraints trusted after the
   bulk-load fast path. PostgreSQL has no such concept — constraints
   created here are validated and trusted immediately — so the block
   has no equivalent and is intentionally omitted.
   ============================================================= */
