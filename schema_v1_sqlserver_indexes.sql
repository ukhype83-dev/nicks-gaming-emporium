-- Nick's Gaming Emporium — V1 nonclustered indexes (applied AFTER the bulk load)

/* dbo.releases */
CREATE INDEX ix_releases_platform        ON dbo.releases(platform_id);
CREATE INDEX ix_releases_normalised      ON dbo.releases(normalised_title);
CREATE INDEX ix_releases_first_release   ON dbo.releases(first_release_date);
GO

/* dbo.hardware */
CREATE INDEX ix_hardware_platform ON dbo.hardware(platform_id);
CREATE INDEX ix_hardware_revision ON dbo.hardware(revision_of);
GO

/* dbo.shop_addresses */
CREATE UNIQUE INDEX ux_shop_addresses_one_per_shop ON dbo.shop_addresses(shop_id);
GO

/* dbo.inventory — split filtered uniques: software (release_id) XOR hardware (hardware_id) */
CREATE UNIQUE INDEX ux_inventory_shop_release_cond
    ON dbo.inventory(shop_id, release_id, condition)
    WHERE release_id IS NOT NULL;
CREATE UNIQUE INDEX ux_inventory_shop_hardware_cond
    ON dbo.inventory(shop_id, hardware_id, condition)
    WHERE hardware_id IS NOT NULL;
GO

/* dbo.inventory_movements */
CREATE INDEX ix_inv_mov_occurred  ON dbo.inventory_movements(occurred_at);
CREATE INDEX ix_inv_mov_inventory ON dbo.inventory_movements(inventory_id, occurred_at);
GO

/* dbo.customers */
CREATE INDEX ix_customers_regime_status ON dbo.customers(governing_regime, status);
CREATE INDEX ix_customers_signed_up     ON dbo.customers(signed_up_at);
GO

/* dbo.customer_addresses */
CREATE INDEX ix_cust_addr_customer ON dbo.customer_addresses(customer_id);
CREATE INDEX ix_cust_addr_hash     ON dbo.customer_addresses(address_hash);
CREATE UNIQUE INDEX ux_cust_addr_current
    ON dbo.customer_addresses(customer_id, address_type)
    WHERE effective_to IS NULL;
GO

/* dbo.customer_emails */
CREATE INDEX ix_cust_emails_customer ON dbo.customer_emails(customer_id);
GO

/* dbo.communication_preferences */
CREATE UNIQUE INDEX ux_comm_pref_cust_chan_purpose
    ON dbo.communication_preferences(customer_id, channel, purpose);
GO

/* dbo.consent_events */
CREATE INDEX ix_consent_events_cust_time
    ON dbo.consent_events(customer_id, occurred_at);
GO

/* dbo.customer_lifecycle_events */
CREATE INDEX ix_lifecycle_cust_time ON dbo.customer_lifecycle_events(customer_id, occurred_at);
CREATE INDEX ix_lifecycle_type_time ON dbo.customer_lifecycle_events(event_type, occurred_at);
GO

/* dbo.loyalty_memberships */
CREATE INDEX ix_loyalty_customer ON dbo.loyalty_memberships(customer_id);
GO

/* dbo.saved_payment_methods */
CREATE INDEX ix_saved_pay_customer ON dbo.saved_payment_methods(customer_id);
GO

/* dbo.transactions — ix_tx_customer_time is filtered: customer_id is NULL on anonymous walk-in sales */
CREATE INDEX ix_tx_occurred       ON dbo.transactions(occurred_at);
CREATE INDEX ix_tx_shop_time      ON dbo.transactions(shop_id, occurred_at);
CREATE INDEX ix_tx_customer_time  ON dbo.transactions(customer_id, occurred_at)
    WHERE customer_id IS NOT NULL;
CREATE INDEX ix_tx_channel_time   ON dbo.transactions(channel, occurred_at);
CREATE INDEX ix_tx_source         ON dbo.transactions(source_system, occurred_at);
-- filtered: only return receipts carry a non-NULL original_transaction_id
CREATE INDEX ix_tx_original       ON dbo.transactions(original_transaction_id)
    WHERE original_transaction_id IS NOT NULL;
GO

/* dbo.transaction_lines */
CREATE INDEX ix_txl_transaction ON dbo.transaction_lines(transaction_id);
CREATE INDEX ix_txl_release     ON dbo.transaction_lines(release_id);
-- filtered: hardware lines only
CREATE INDEX ix_txl_hardware    ON dbo.transaction_lines(hardware_id)
    WHERE hardware_id IS NOT NULL;
GO

/* dbo.payments */
CREATE INDEX ix_pay_transaction ON dbo.payments(transaction_id);
GO

/* dbo.trade_ins — ix_trade_ins_customer is filtered: walk-in trade-ins have NULL customer_id */
CREATE INDEX ix_trade_ins_occurred ON dbo.trade_ins(occurred_at);
CREATE INDEX ix_trade_ins_customer ON dbo.trade_ins(customer_id, occurred_at)
    WHERE customer_id IS NOT NULL;
GO

/* dbo.trade_in_items */
CREATE INDEX ix_trade_in_items_trade_in ON dbo.trade_in_items(trade_in_id);
CREATE INDEX ix_trade_in_items_hardware ON dbo.trade_in_items(hardware_id)
    WHERE hardware_id IS NOT NULL;
GO

/* dbo.store_credit_ledger */
CREATE INDEX ix_store_credit_customer ON dbo.store_credit_ledger(customer_id, occurred_at);
GO

/* hr.employment_spells */
CREATE INDEX ix_spells_person     ON hr.employment_spells(person_id);
CREATE INDEX ix_spells_active     ON hr.employment_spells(started_at, ended_at);
CREATE INDEX ix_spells_shop       ON hr.employment_spells(home_shop_id);
GO

/* hr.contracts */
CREATE INDEX ix_contracts_spell ON hr.contracts(spell_id);
GO

/* hr.staff_addresses */
CREATE INDEX ix_staff_addr_person ON hr.staff_addresses(person_id);
GO

/* hr.staff_shifts */
CREATE INDEX ix_shifts_shop_time  ON hr.staff_shifts(shop_id, shift_start);
CREATE INDEX ix_shifts_spell_time ON hr.staff_shifts(spell_id, shift_start);
GO

/* hr.payroll_runs */
CREATE INDEX ix_payroll_runs_period ON hr.payroll_runs(country_code, period_end);
GO

/* hr.payroll_lines */
CREATE INDEX ix_payroll_lines_spell ON hr.payroll_lines(spell_id);
CREATE INDEX ix_payroll_lines_run   ON hr.payroll_lines(payroll_run_id);
GO

-- FK supporting indexes not already covered above

-- filtered unique: at most one is_primary=1 row per customer
CREATE UNIQUE INDEX ux_customer_emails_primary
    ON dbo.customer_emails(customer_id)
    WHERE is_primary = 1;
GO

/* dbo.transactions — staff_id + shipping_address_id FKs, both filtered on NOT NULL */
CREATE INDEX ix_tx_staff               ON dbo.transactions(staff_id)
    WHERE staff_id IS NOT NULL;
CREATE INDEX ix_tx_shipping_addr       ON dbo.transactions(shipping_address_id)
    WHERE shipping_address_id IS NOT NULL;
GO

/* dbo.payments — saved_payment_id FK (filtered on NOT NULL) */
CREATE INDEX ix_payments_saved         ON dbo.payments(saved_payment_id)
    WHERE saved_payment_id IS NOT NULL;
GO

/* dbo.trade_ins — shop_id / transaction_id / staff_id FKs (staff_id filtered on NOT NULL) */
CREATE INDEX ix_trade_ins_shop         ON dbo.trade_ins(shop_id);
CREATE INDEX ix_trade_ins_tx           ON dbo.trade_ins(transaction_id);
CREATE INDEX ix_trade_ins_staff        ON dbo.trade_ins(staff_id)
    WHERE staff_id IS NOT NULL;
GO

/* dbo.trade_in_items — release FK */
CREATE INDEX ix_trade_in_items_release ON dbo.trade_in_items(release_id);
GO

/* hr.employment_spells — role / dept / pay_grade FKs */
CREATE INDEX ix_emp_spells_role        ON hr.employment_spells(role_id);
CREATE INDEX ix_emp_spells_dept        ON hr.employment_spells(department_id);
CREATE INDEX ix_emp_spells_pay_grade   ON hr.employment_spells(pay_grade_id);
GO

/* closure-era tables */
CREATE INDEX ix_shops_closed_date      ON dbo.shops(closed_date) WHERE closed_date IS NOT NULL;
CREATE INDEX ix_emp_spells_ended       ON hr.employment_spells(ended_at) WHERE ended_at IS NOT NULL;
CREATE INDEX ix_comp_history_person    ON hr.compensation_history(person_id, effective_from);
CREATE INDEX ix_comp_history_reason    ON hr.compensation_history(change_reason);
GO

-- web (community) layer indexes
CREATE INDEX ix_reviews_release        ON web.reviews(release_id, posted_at) WHERE release_id IS NOT NULL;
CREATE INDEX ix_reviews_hardware       ON web.reviews(hardware_id, posted_at) WHERE hardware_id IS NOT NULL;
CREATE INDEX ix_reviews_account        ON web.reviews(account_id, posted_at);
CREATE INDEX ix_reviews_posted         ON web.reviews(posted_at);
GO

CREATE INDEX ix_comments_review        ON web.review_comments(review_id, posted_at);
CREATE INDEX ix_comments_account       ON web.review_comments(account_id, posted_at);
GO

CREATE INDEX ix_votes_review           ON web.review_votes(review_id);
GO

CREATE INDEX ix_pv_occurred            ON web.page_views(occurred_at);
CREATE INDEX ix_pv_session             ON web.page_views(session_id, occurred_at);
CREATE INDEX ix_pv_path                ON web.page_views(url_path, occurred_at);
CREATE INDEX ix_pv_account             ON web.page_views(account_id, occurred_at) WHERE account_id IS NOT NULL;
GO

-- Re-trust CHECK/FK constraints left NOT TRUSTED by the bulk loader so the
-- optimizer can use them. Walks sys.check_constraints + sys.foreign_keys and
-- issues WITH CHECK CHECK CONSTRAINT against each untrusted one. Idempotent.

DECLARE @trust_sql NVARCHAR(MAX) = N'';

SELECT @trust_sql = @trust_sql +
    N'ALTER TABLE [' + OBJECT_SCHEMA_NAME(parent_object_id)
    + N'].[' + OBJECT_NAME(parent_object_id)
    + N'] WITH CHECK CHECK CONSTRAINT [' + name + N'];' + CHAR(10)
FROM sys.check_constraints
WHERE is_not_trusted = 1 AND is_disabled = 0;

SELECT @trust_sql = @trust_sql +
    N'ALTER TABLE [' + OBJECT_SCHEMA_NAME(parent_object_id)
    + N'].[' + OBJECT_NAME(parent_object_id)
    + N'] WITH CHECK CHECK CONSTRAINT [' + name + N'];' + CHAR(10)
FROM sys.foreign_keys
WHERE is_not_trusted = 1 AND is_disabled = 0;

IF LEN(@trust_sql) > 0
    EXEC sp_executesql @trust_sql;
GO
