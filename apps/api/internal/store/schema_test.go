package store_test

// Acceptance tests for the phase 1 schema: one test per criterion in
// docs/design/data-model.md, "Acceptance criteria for the migration", run as
// jade_api against a real Postgres.

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/dbtest"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/store"
)

// fillTenant gives the tenant at least one row in every tenant table.
func fillTenant(t *testing.T, api *pgxpool.Pool, ten tenant) {
	t.Helper()
	ctx := t.Context()
	mustInTenant(t, api, ten, func(tx pgx.Tx) error {
		customer := newCustomer(t, tx, ten)
		invoice := newDraft(t, tx, ten, customer, "invoice", nil)
		for _, role := range []string{"seller", "buyer"} {
			if _, err := tx.Exec(ctx, "insert into app.invoice_parties (tenant_id, invoice_id, role, name) values ($1, $2, $3, 'Example')",
				ten.ID, invoice, role); err != nil {
				return err
			}
		}
		mustIssue(t, tx, ten, invoice)

		var event, endpoint, delivery uuid.UUID
		steps := []struct {
			sql  string
			args []any
			dest *uuid.UUID
		}{
			{"insert into app.seller_profiles (tenant_id, legal_name) values ($1, 'Example Seller GmbH')", []any{ten.ID}, nil},
			{"insert into app.api_keys (tenant_id, name, prefix, secret_hash) values ($1, 'test', $2, $3)",
				[]any{ten.ID, randomHex(t, 6), make([]byte, 32)}, nil},
			{`insert into app.invoice_files (tenant_id, invoice_id, kind, storage_path, content_type, byte_size, sha256)
			  values ($1, $2, 'pdf', 'test/invoice.pdf', 'application/pdf', 1, $3)`, []any{ten.ID, invoice, make([]byte, 32)}, nil},
			{"insert into app.payments (tenant_id, invoice_id, amount, paid_on) values ($1, $2, 1, $3)", []any{ten.ID, invoice, issueDay}, nil},
			{`insert into app.idempotency_keys (tenant_id, key, method, path, request_hash, response_status, response_body, expires_at)
			  values ($1, $2, 'POST', '/v1/customers', $3, 201, $4, now() + interval '1 day')`,
				[]any{ten.ID, randomHex(t, 8), make([]byte, 32), []byte("{}")}, nil},
			{`insert into app.events (tenant_id, type, subject_type, subject_id, payload)
			  values ($1, 'invoice.issued', 'invoice', $2, '{}') returning id`, []any{ten.ID, invoice}, &event},
			{`insert into app.webhook_endpoints (tenant_id, url, secret_ciphertext, secret_key_id)
			  values ($1, 'https://hooks.example.com/jade', $2, 'k1') returning id`, []any{ten.ID, []byte{1}}, &endpoint},
		}
		for _, s := range steps {
			var err error
			if s.dest != nil {
				err = tx.QueryRow(ctx, s.sql, s.args...).Scan(s.dest)
			} else {
				_, err = tx.Exec(ctx, s.sql, s.args...)
			}
			if err != nil {
				return fmt.Errorf("%s: %w", s.sql, err)
			}
		}
		if err := tx.QueryRow(ctx, `insert into app.webhook_deliveries (tenant_id, event_id, endpoint_id)
			values ($1, $2, $3) returning id`, ten.ID, event, endpoint).Scan(&delivery); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into app.webhook_attempts (tenant_id, delivery_id, attempt_no, started_at)
			values ($1, $2, 1, now())`, ten.ID, delivery); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into app.audit_log (tenant_id, actor_type, action, subject_type, subject_id)
			values ($1, 'system', 'invoice.issue', 'invoice', $2)`, ten.ID, invoice)
		return err
	})
}

// tenantTables lists every table in app that belongs to a tenant, from the
// catalog, so a table added later is covered without editing the test.
func tenantTables(t *testing.T, admin *pgxpool.Pool) []string {
	t.Helper()
	rows, err := admin.Query(t.Context(), `
		select relname from pg_class
		where relnamespace = 'app'::regnamespace and relkind in ('r', 'p') and relname <> 'tax_rates'
		order by relname`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) < 17 {
		t.Fatalf("found %d tenant tables, want at least 17", len(tables))
	}
	return tables
}

// tenantColumn is the column that holds the tenant.
func tenantColumn(table string) string {
	if table == "tenants" {
		return "id"
	}
	return "tenant_id"
}

// Criterion 1: with tenant A set, no table returns or accepts tenant B's
// rows; with no tenant set, every tenant table returns no rows.
func TestTenantIsolation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	admin, api := dbtest.AdminPool(t), dbtest.APIPool(t)
	a, b := newTenant(t, admin, "DE"), newTenant(t, admin, "DE")
	fillTenant(t, api, a)
	fillTenant(t, api, b)

	t.Run("every table has forced RLS and the tenant policy", func(t *testing.T) {
		rows, err := admin.Query(ctx, `
			select c.relname, c.relrowsecurity and c.relforcerowsecurity,
			  coalesce((select bool_and(p.qual like '%current_tenant_id()%' and p.with_check like '%current_tenant_id()%' and p.cmd = 'ALL')
			            from pg_policies p where p.schemaname = 'app' and p.tablename = c.relname), false)
			from pg_class c
			where c.relnamespace = 'app'::regnamespace and c.relkind in ('r', 'p') and c.relname <> 'tax_rates'`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var table string
			var forced, policy bool
			if err := rows.Scan(&table, &forced, &policy); err != nil {
				t.Fatal(err)
			}
			if !forced || !policy {
				t.Errorf("%s: forced RLS %v, tenant policy %v", table, forced, policy)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	})

	for _, table := range tenantTables(t, admin) {
		col := tenantColumn(table)
		t.Run(table, func(t *testing.T) {
			var bRows int
			if err := admin.QueryRow(ctx, fmt.Sprintf("select count(*) from app.%s where %s = $1", table, col), b.ID).Scan(&bRows); err != nil {
				t.Fatal(err)
			}
			if bRows == 0 {
				t.Fatalf("fixture gave tenant B no rows in %s; extend fillTenant", table)
			}

			var foreign, own int
			mustInTenant(t, api, a, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx, fmt.Sprintf("select count(*) filter (where %s <> $1), count(*) from app.%s", col, table), a.ID).
					Scan(&foreign, &own)
			})
			if foreign != 0 {
				t.Errorf("tenant A sees %d rows of other tenants", foreign)
			}
			if own == 0 {
				t.Errorf("tenant A sees none of its own rows")
			}

			var unset int
			if err := api.QueryRow(ctx, fmt.Sprintf("select count(*) from app.%s", table)).Scan(&unset); err != nil {
				t.Fatal(err)
			}
			if unset != 0 {
				t.Errorf("with no tenant set, %d rows are visible", unset)
			}
		})
	}

	t.Run("writes to another tenant are rejected or match nothing", func(t *testing.T) {
		err := inTenant(t, api, a, exec(t, "insert into app.customers (tenant_id, kind, name) values ($1, 'business', 'X')", b.ID))
		wantSQLState(t, err, errPrivilege)

		err = inTenant(t, api, a, exec(t, "update app.customers set tenant_id = $1 where tenant_id = $2", b.ID, a.ID))
		wantSQLState(t, err, errPrivilege)

		var updated, deleted int64
		mustInTenant(t, api, a, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, "update app.customers set name = 'taken over' where tenant_id = $1", b.ID)
			updated = tag.RowsAffected()
			if err != nil {
				return err
			}
			tag, err = tx.Exec(ctx, "delete from app.idempotency_keys where tenant_id = $1", b.ID)
			deleted = tag.RowsAffected()
			return err
		})
		if updated != 0 || deleted != 0 {
			t.Errorf("tenant A changed %d and deleted %d of tenant B's rows", updated, deleted)
		}

		err = inTenant(t, api, a, exec(t, "update app.tenants set name = 'taken over' where id = $1", b.ID))
		if err != nil {
			t.Fatal(err)
		}
		var name string
		if err := admin.QueryRow(ctx, "select name from app.tenants where id = $1", b.ID).Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name == "taken over" {
			t.Error("tenant A renamed tenant B")
		}
	})
}

// Criterion 2: a foreign key to another tenant's customer, series, or invoice
// is rejected.
func TestCrossTenantReferences(t *testing.T) {
	t.Parallel()
	admin, api := dbtest.AdminPool(t), dbtest.APIPool(t)
	a, b := newTenant(t, admin, "DE"), newTenant(t, admin, "DE")

	var aCustomer, bCustomer, bInvoice uuid.UUID
	mustInTenant(t, api, a, func(tx pgx.Tx) error { aCustomer = newCustomer(t, tx, a); return nil })
	mustInTenant(t, api, b, func(tx pgx.Tx) error {
		bCustomer = newCustomer(t, tx, b)
		bInvoice = newDraft(t, tx, b, bCustomer, "invoice", nil)
		mustIssue(t, tx, b, bInvoice)
		return nil
	})

	insert := `insert into app.invoices (tenant_id, document_type, customer_id, series_id, preceding_invoice_id, jurisdiction, currency)
		values ($1, $2, $3, $4, $5, 'DE', 'EUR')`
	cases := []struct {
		name             string
		docType          string
		customer, series uuid.UUID
		preceding        *uuid.UUID
	}{
		{"customer", "invoice", bCustomer, a.SeriesID, nil},
		{"number series", "invoice", aCustomer, b.SeriesID, nil},
		{"preceding invoice", "credit_note", aCustomer, a.SeriesID, &bInvoice},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := inTenant(t, api, a, exec(t, insert, a.ID, c.docType, c.customer, c.series, c.preceding))
			wantSQLState(t, err, errForeignKey)
		})
	}

	t.Run("invoice line", func(t *testing.T) {
		err := inTenant(t, api, a, exec(t, `insert into app.invoice_lines
			(tenant_id, invoice_id, position, name, quantity, unit_price, tax_rate, line_net_amount)
			values ($1, $2, 2, 'X', 1, 1, 19, 1)`, a.ID, bInvoice))
		wantSQLState(t, err, errForeignKey)
	})

	// The same statement with tenant A's own rows works, so the failures
	// above come from the tenant in the key.
	if err := inTenant(t, api, a, exec(t, insert, a.ID, "invoice", aCustomer, a.SeriesID, nil)); err != nil {
		t.Fatal(err)
	}
}

// Criterion 3: an issued invoice and its children cannot change; the only
// status changes are issued to voided or corrected, once.
func TestIssuedInvoiceIsImmutable(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	admin, api := dbtest.AdminPool(t), dbtest.APIPool(t)
	ten := newTenant(t, admin, "DE")

	var customer, issued, draft uuid.UUID
	mustInTenant(t, api, ten, func(tx pgx.Tx) error {
		customer = newCustomer(t, tx, ten)
		issued = newDraft(t, tx, ten, customer, "invoice", nil)
		if _, err := tx.Exec(ctx, "insert into app.invoice_parties (tenant_id, invoice_id, role, name) values ($1, $2, 'buyer', 'B')", ten.ID, issued); err != nil {
			return err
		}
		mustIssue(t, tx, ten, issued)
		draft = newDraft(t, tx, ten, customer, "invoice", nil)
		return nil
	})

	rejected := []struct {
		name string
		sql  string
		code string
	}{
		{"update a content column", "update app.invoices set note = 'changed' where id = $1", errImmutable},
		{"update a total", "update app.invoices set prepaid_amount = 1, amount_due = amount_due - 1 where id = $1", errImmutable},
		{"change the number", "update app.invoices set number = 'X-1' where id = $1", errImmutable},
		{"delete", "delete from app.invoices where id = $1", errImmutable},
		{"insert a line", `insert into app.invoice_lines (tenant_id, invoice_id, position, name, quantity, unit_price, tax_rate, line_net_amount)
			select tenant_id, id, 2, 'X', 1, 1, 19, 1 from app.invoices where id = $1`, errImmutable},
		{"update a line", "update app.invoice_lines set name = 'changed' where invoice_id = $1", errImmutable},
		{"delete a line", "delete from app.invoice_lines where invoice_id = $1", errImmutable},
		{"insert a breakdown", `insert into app.invoice_tax_breakdowns (tenant_id, invoice_id, tax_category, tax_rate, taxable_amount, tax_amount)
			select tenant_id, id, 'S', 7, 0, 0 from app.invoices where id = $1`, errImmutable},
		{"update a breakdown", "update app.invoice_tax_breakdowns set tax_amount = 0 where invoice_id = $1", errImmutable},
		{"delete a breakdown", "delete from app.invoice_tax_breakdowns where invoice_id = $1", errImmutable},
		{"insert a party", "insert into app.invoice_parties (tenant_id, invoice_id, role) select tenant_id, id, 'seller' from app.invoices where id = $1", errImmutable},
		// jade_api has no UPDATE or DELETE on parties at all.
		{"update a party", "update app.invoice_parties set name = 'changed' where invoice_id = $1", errPrivilege},
		{"delete a party", "delete from app.invoice_parties where invoice_id = $1", errPrivilege},
	}
	for _, c := range rejected {
		t.Run(c.name, func(t *testing.T) {
			wantSQLState(t, inTenant(t, api, ten, exec(t, c.sql, issued)), c.code)
		})
	}

	t.Run("parties are protected from postgres too", func(t *testing.T) {
		_, err := admin.Exec(ctx, "update app.invoice_parties set name = 'changed' where invoice_id = $1", issued)
		wantSQLState(t, err, errImmutable)
	})

	void := "update app.invoices set status = 'voided', voided_at = now(), void_reason = 'test' where id = $1"
	correct := "update app.invoices set status = 'corrected', corrected_at = now() where id = $1"

	t.Run("draft cannot become voided or corrected", func(t *testing.T) {
		wantSQLState(t, inTenant(t, api, ten, exec(t, void, draft)), errImmutable)
		wantSQLState(t, inTenant(t, api, ten, exec(t, correct, draft)), errImmutable)
	})

	t.Run("issued to voided, once", func(t *testing.T) {
		if err := inTenant(t, api, ten, exec(t, void, issued)); err != nil {
			t.Fatal(err)
		}
		wantSQLState(t, inTenant(t, api, ten, exec(t, void, issued)), errImmutable)
		wantSQLState(t, inTenant(t, api, ten, exec(t, correct, issued)), errImmutable)
		wantSQLState(t, inTenant(t, api, ten, exec(t, "update app.invoices set void_reason = 'other' where id = $1", issued)), errImmutable)
	})

	t.Run("issued to corrected, once, with one issued correction", func(t *testing.T) {
		var original, first, second uuid.UUID
		mustInTenant(t, api, ten, func(tx pgx.Tx) error {
			original = newDraft(t, tx, ten, customer, "invoice", nil)
			mustIssue(t, tx, ten, original)
			first = newDraft(t, tx, ten, customer, "corrected_invoice", &original)
			second = newDraft(t, tx, ten, customer, "corrected_invoice", &original)
			mustIssue(t, tx, ten, first)
			_, err := tx.Exec(ctx, correct, original)
			return err
		})
		wantSQLState(t, inTenant(t, api, ten, exec(t, correct, original)), errImmutable)
		wantSQLState(t, inTenant(t, api, ten, exec(t, void, original)), errImmutable)

		err := inTenant(t, api, ten, func(tx pgx.Tx) error {
			_, err := issue(t, tx, ten, second)
			return err
		})
		wantConstraint(t, err, errUnique, "invoices_one_issued_correction")
	})
}

// Criterion 4: 50 concurrent issues on one series get exactly the numbers 1
// to 50, and an issue that rolls back leaves no gap.
func TestNumberingIsGaplessUnderConcurrency(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	admin, api := dbtest.AdminPool(t), dbtest.APIPool(t)
	ten := newTenant(t, admin, "DE")

	const n = 50
	var drafts []uuid.UUID
	mustInTenant(t, api, ten, func(tx pgx.Tx) error {
		customer := newCustomer(t, tx, ten)
		for range n + 2 {
			drafts = append(drafts, newDraft(t, tx, ten, customer, "invoice", nil))
		}
		return nil
	})

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for _, id := range drafts[:n] {
		wg.Go(func() {
			errs <- inTenant(t, api, ten, func(tx pgx.Tx) error {
				_, err := issue(t, tx, ten, id)
				return err
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	numbers := func() []string {
		var got []string
		mustInTenant(t, api, ten, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, "select number from app.invoices where number is not null order by sequence_value")
			if err != nil {
				return err
			}
			got, err = pgx.CollectRows(rows, pgx.RowTo[string])
			return err
		})
		return got
	}
	want := make([]string, n)
	for i := range want {
		want[i] = fmt.Sprint(i + 1)
	}
	if got := numbers(); !slices.Equal(got, want) {
		t.Fatalf("numbers = %v, want 1 to %d", got, n)
	}

	errRollback := errors.New("roll back on purpose")
	err := inTenant(t, api, ten, func(tx pgx.Tx) error {
		if _, err := issue(t, tx, ten, drafts[n]); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatal(err)
	}
	var number string
	mustInTenant(t, api, ten, func(tx pgx.Tx) error {
		var err error
		number, err = issue(t, tx, ten, drafts[n+1])
		return err
	})
	if number != fmt.Sprint(n+1) {
		t.Errorf("number after a rolled-back issue = %s, want %d", number, n+1)
	}
}

// Criterion 5: totals that break a BR-CO formula, and amounts with more
// decimals than the currency allows, are rejected.
func TestTotalsAndMinorUnits(t *testing.T) {
	t.Parallel()
	admin, api := dbtest.AdminPool(t), dbtest.APIPool(t)
	de, jp := newTenant(t, admin, "DE"), newTenant(t, admin, "JP")

	var deCustomer, deDraft, jpCustomer, jpDraft, jpIssued uuid.UUID
	mustInTenant(t, api, de, func(tx pgx.Tx) error {
		deCustomer = newCustomer(t, tx, de)
		deDraft = newDraft(t, tx, de, deCustomer, "invoice", nil)
		return nil
	})
	mustInTenant(t, api, jp, func(tx pgx.Tx) error {
		jpCustomer = newCustomer(t, tx, jp)
		jpDraft = newDraft(t, tx, jp, jpCustomer, "invoice", nil)
		jpIssued = newDraft(t, tx, jp, jpCustomer, "invoice", nil)
		mustIssue(t, tx, jp, jpIssued)
		return nil
	})

	insert := `insert into app.invoices (tenant_id, document_type, customer_id, series_id, jurisdiction, currency,
		line_net_total, tax_exclusive_total, tax_total, tax_inclusive_total, prepaid_amount, amount_due)
		values ($1, 'invoice', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	type totals struct{ lineNet, taxExclusive, tax, taxInclusive, prepaid, due string }
	run := func(ten tenant, customer uuid.UUID, v totals) error {
		return inTenant(t, api, ten, exec(t, insert, ten.ID, customer, ten.SeriesID, ten.Jurisdiction, ten.Currency,
			v.lineNet, v.taxExclusive, v.tax, v.taxInclusive, v.prepaid, v.due))
	}

	if err := run(de, deCustomer, totals{"100.01", "100.01", "19.00", "119.01", "10.00", "109.01"}); err != nil {
		t.Fatalf("consistent totals rejected: %v", err)
	}
	for name, v := range map[string]totals{
		"BR-CO-13 tax exclusive total": {"100.00", "99.00", "19.00", "118.00", "0", "118.00"},
		"BR-CO-15 tax inclusive total": {"100.00", "100.00", "19.00", "120.00", "0", "120.00"},
		"BR-CO-16 amount due":          {"100.00", "100.00", "19.00", "119.00", "10.00", "119.00"},
	} {
		t.Run(name, func(t *testing.T) {
			wantConstraint(t, run(de, deCustomer, v), errCheck, "invoices_br_co_totals")
		})
	}

	// Rejected, not rounded: the amount columns have no scale that would
	// round 100.005 to 100.01 before the check runs.
	t.Run("euro total with a fraction of a cent", func(t *testing.T) {
		err := run(de, deCustomer, totals{"100.005", "100.005", "19", "119.005", "0", "119.005"})
		wantConstraint(t, err, errCheck, "invoices_amounts_fit_currency")
	})
	t.Run("euro line with a fraction of a cent", func(t *testing.T) {
		err := inTenant(t, api, de, exec(t, `insert into app.invoice_lines
			(tenant_id, invoice_id, position, name, quantity, unit_price, tax_rate, line_net_amount)
			values ($1, $2, 2, 'X', 1, 1.001, 19, 1.001)`, de.ID, deDraft))
		wantSQLState(t, err, errMinorUnits)
	})
	t.Run("amount over 16 integer digits", func(t *testing.T) {
		big := "10000000000000000"
		err := run(de, deCustomer, totals{big, big, "0", big, "0", big})
		wantConstraint(t, err, errCheck, "invoices_amounts_fit_currency")
	})
	t.Run("yen total with decimals", func(t *testing.T) {
		err := run(jp, jpCustomer, totals{"100.5", "100.5", "10", "110.5", "0", "110.5"})
		wantConstraint(t, err, errCheck, "invoices_amounts_fit_currency")
	})
	t.Run("yen line with decimals", func(t *testing.T) {
		err := inTenant(t, api, jp, exec(t, `insert into app.invoice_lines
			(tenant_id, invoice_id, position, name, quantity, unit_price, tax_rate, line_net_amount)
			values ($1, $2, 2, 'X', 1, 0.5, 10, 0.5)`, jp.ID, jpDraft))
		wantSQLState(t, err, errMinorUnits)
	})
	t.Run("yen tax amount with decimals", func(t *testing.T) {
		err := inTenant(t, api, jp, exec(t, "update app.invoice_tax_breakdowns set tax_amount = 10.5 where invoice_id = $1", jpDraft))
		wantSQLState(t, err, errMinorUnits)
	})
	t.Run("yen payment with decimals", func(t *testing.T) {
		err := inTenant(t, api, jp, exec(t, "insert into app.payments (tenant_id, invoice_id, amount, paid_on) values ($1, $2, 0.5, $3)",
			jp.ID, jpIssued, issueDay))
		wantSQLState(t, err, errMinorUnits)
	})
}

// Criterion 6: UPDATE and DELETE on insert-only tables fail, for jade_api
// (no grant) and for postgres (trigger).
func TestInsertOnlyTables(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	admin, api := dbtest.AdminPool(t), dbtest.APIPool(t)
	ten := newTenant(t, admin, "DE")
	fillTenant(t, api, ten)

	for _, table := range []string{"invoice_files", "payments", "events", "webhook_attempts", "audit_log"} {
		t.Run(table, func(t *testing.T) {
			update := fmt.Sprintf("update app.%s set created_at = created_at where tenant_id = $1", table)
			if table == "audit_log" {
				update = "update app.audit_log set details = details where tenant_id = $1"
			}
			del := fmt.Sprintf("delete from app.%s where tenant_id = $1", table)

			wantSQLState(t, inTenant(t, api, ten, exec(t, update, ten.ID)), errPrivilege)
			wantSQLState(t, inTenant(t, api, ten, exec(t, del, ten.ID)), errPrivilege)

			// As postgres, inside a transaction that is always rolled back, so
			// a broken trigger cannot wipe the table. CASCADE gets past the
			// foreign keys that would otherwise stop TRUNCATE before the
			// trigger runs.
			for _, s := range []struct {
				sql  string
				args []any
			}{
				{update, []any{ten.ID}},
				{del, []any{ten.ID}},
				{fmt.Sprintf("truncate app.%s cascade", table), nil},
			} {
				err := pgx.BeginFunc(ctx, admin, func(tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, s.sql, s.args...); err != nil {
						return err
					}
					return errors.New("statement succeeded")
				})
				wantSQLState(t, err, errImmutable)
			}
		})
	}
}

// Criterion 7: app.find_api_key works with no tenant set, is the only way
// jade_api can read another tenant's row, and returns revocation and expiry.
func TestFindAPIKey(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	admin, api := dbtest.AdminPool(t), dbtest.APIPool(t)
	a, b := newTenant(t, admin, "DE"), newTenant(t, admin, "DE")

	prefix := randomHex(t, 6)
	var keyID uuid.UUID
	mustInTenant(t, api, b, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `insert into app.api_keys (tenant_id, name, prefix, secret_hash, expires_at, revoked_at)
			values ($1, 'old', $2, $3, now() + interval '30 days', now()) returning id`,
			b.ID, prefix, make([]byte, 32)).Scan(&keyID)
	})

	q := store.New(api)
	var visible int
	if err := api.QueryRow(ctx, "select count(*) from app.api_keys").Scan(&visible); err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatalf("with no tenant set, %d keys are visible", visible)
	}

	key, err := q.FindAPIKey(ctx, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if key.ID != keyID || key.TenantID != b.ID || len(key.SecretHash) != 32 || key.RevokedAt == nil || key.ExpiresAt == nil {
		t.Errorf("FindAPIKey = %+v, want key %s of tenant %s with hash, revocation, and expiry", key, keyID, b.ID)
	}

	// Inside another tenant's transaction it finds the key as well.
	mustInTenant(t, api, a, func(tx pgx.Tx) error {
		k, err := store.New(tx).FindAPIKey(ctx, prefix)
		if err == nil && k.TenantID != b.ID {
			err = fmt.Errorf("tenant %s, want %s", k.TenantID, b.ID)
		}
		return err
	})

	if _, err := q.FindAPIKey(ctx, "unknown"+prefix); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown prefix: err = %v, want pgx.ErrNoRows", err)
	}

	t.Run("no other function in app bypasses RLS for jade_api", func(t *testing.T) {
		rows, err := admin.Query(ctx, `
			select p.oid::regprocedure::text from pg_proc p
			where p.pronamespace = 'app'::regnamespace and p.prosecdef and has_function_privilege('jade_api', p.oid, 'execute')`)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, []string{"app.find_api_key(text)"}) {
			t.Errorf("security definer functions jade_api can run = %v, want only app.find_api_key(text)", got)
		}
	})

	t.Run("last_used_at is written at most once a minute and is not an edit", func(t *testing.T) {
		read := func() (used, updated *string) {
			if err := admin.QueryRow(ctx, "select last_used_at::text, updated_at::text from app.api_keys where id = $1", keyID).
				Scan(&used, &updated); err != nil {
				t.Fatal(err)
			}
			return used, updated
		}
		_, updatedBefore := read()
		touch := func(tx pgx.Tx) error { return store.New(tx).TouchAPIKey(ctx, keyID) }
		mustInTenant(t, api, b, touch)
		first, updatedAfter := read()
		if first == nil {
			t.Fatal("last_used_at not set")
		}
		if *updatedAfter != *updatedBefore {
			t.Errorf("recording use moved updated_at from %s to %s", *updatedBefore, *updatedAfter)
		}
		mustInTenant(t, api, b, touch)
		if second, _ := read(); second == nil || *second != *first {
			t.Errorf("last_used_at moved from %s to %v within a minute", *first, second)
		}
	})
}

// Criterion 8: a payment on a draft, voided, or corrected invoice, or on a
// credit note, is rejected.
func TestPaymentNeedsIssuedInvoice(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	admin, api := dbtest.AdminPool(t), dbtest.APIPool(t)
	ten := newTenant(t, admin, "DE")

	invoices := map[string]uuid.UUID{}
	mustInTenant(t, api, ten, func(tx pgx.Tx) error {
		customer := newCustomer(t, tx, ten)
		invoices["draft"] = newDraft(t, tx, ten, customer, "invoice", nil)
		for _, name := range []string{"issued", "voided", "corrected"} {
			invoices[name] = newDraft(t, tx, ten, customer, "invoice", nil)
			mustIssue(t, tx, ten, invoices[name])
		}
		credit := invoices["issued"]
		invoices["credit note"] = newDraft(t, tx, ten, customer, "credit_note", &credit)
		mustIssue(t, tx, ten, invoices["credit note"])
		if _, err := tx.Exec(ctx, "update app.invoices set status = 'voided', voided_at = now() where id = $1", invoices["voided"]); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "update app.invoices set status = 'corrected', corrected_at = now() where id = $1", invoices["corrected"])
		return err
	})

	pay := "insert into app.payments (tenant_id, invoice_id, amount, paid_on) values ($1, $2, 10, $3)"
	for _, name := range []string{"draft", "voided", "corrected", "credit note"} {
		t.Run(name, func(t *testing.T) {
			wantSQLState(t, inTenant(t, api, ten, exec(t, pay, ten.ID, invoices[name], issueDay)), errNotPayable)
		})
	}
	if err := inTenant(t, api, ten, exec(t, pay, ten.ID, invoices["issued"], issueDay)); err != nil {
		t.Errorf("payment on an issued invoice: %v", err)
	}
}

// Criterion 9: anon and authenticated have no privileges on schema app or
// anything in it, nor on the webhook queue, whose messages carry tenant IDs.
func TestNoAccessForSupabaseAPIRoles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	admin := dbtest.AdminPool(t)

	rows, err := admin.Query(ctx, `
		with roles(r) as (values ('anon'), ('authenticated'))
		select r || ': schema app ' || priv from roles, unnest(array['usage', 'create']) priv
		  where has_schema_privilege(r, 'app', priv)
		union all
		select r || ': ' || c.oid::regclass::text || ' ' || priv
		  from roles, pg_class c, unnest(array['select', 'insert', 'update', 'delete', 'truncate', 'references', 'trigger']) priv
		  where c.relnamespace = 'app'::regnamespace and c.relkind in ('r', 'p', 'v', 'm', 'f')
		    and has_table_privilege(r, c.oid, priv)
		union all
		select r || ': ' || c.oid::regclass::text || ' ' || priv
		  from roles, pg_class c, unnest(array['usage', 'select', 'update']) priv
		  where c.relnamespace = 'app'::regnamespace and c.relkind = 'S'
		    and has_sequence_privilege(r, c.oid, priv)
		union all
		select r || ': ' || p.oid::regprocedure::text || ' execute'
		  from roles, pg_proc p
		  where p.pronamespace = 'app'::regnamespace and has_function_privilege(r, p.oid, 'execute')
		union all
		select r || ': schema pgmq usage' from roles where has_schema_privilege(r, 'pgmq', 'usage')
		union all
		select r || ': ' || c.oid::regclass::text || ' ' || priv
		  from roles, pg_class c, unnest(array['select', 'insert', 'update', 'delete', 'truncate']) priv
		  where c.oid in ('pgmq.q_webhook_deliveries'::regclass, 'pgmq.a_webhook_deliveries'::regclass)
		    and has_table_privilege(r, c.oid, priv)`)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range grants {
		t.Error(g)
	}
}

// The API and the worker use the webhook queue as jade_api (ADR 0003): the
// grants cover send, read, set_vt, archive, and delete, and nothing else in
// pgmq.
func TestWebhookQueueGrants(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	api := dbtest.APIPool(t)

	err := pgx.BeginFunc(ctx, api, func(tx pgx.Tx) error {
		var first, second int64
		if err := tx.QueryRow(ctx, `select pgmq.send('webhook_deliveries', '{"delivery_id": "test"}')`).Scan(&first); err != nil {
			return fmt.Errorf("send: %w", err)
		}
		if err := tx.QueryRow(ctx, `select pgmq.send('webhook_deliveries', '{"delivery_id": "test"}')`).Scan(&second); err != nil {
			return fmt.Errorf("send: %w", err)
		}
		if _, err := tx.Exec(ctx, "select * from pgmq.read('webhook_deliveries', 30, 1)"); err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if _, err := tx.Exec(ctx, "select * from pgmq.set_vt('webhook_deliveries', $1, 60)", first); err != nil {
			return fmt.Errorf("set_vt: %w", err)
		}
		for name, sql := range map[string]string{
			"archive": "select pgmq.archive('webhook_deliveries', $1::bigint)",
			"delete":  "select pgmq.delete('webhook_deliveries', $1::bigint)",
		} {
			id := first
			if name == "delete" {
				id = second
			}
			var ok bool
			if err := tx.QueryRow(ctx, sql, id).Scan(&ok); err != nil || !ok {
				return fmt.Errorf("%s: %v, %w", name, ok, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = pgx.BeginFunc(ctx, api, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "select pgmq.create('jade_api_must_not_create_queues')")
		return err
	})
	wantSQLState(t, err, errPrivilege)
}

// From the security review: jade_api may only revoke a key and count a series
// up by one. A bug cannot replace a key's hash, undo a revocation, or make a
// gap in the invoice numbers by moving the counter.
func TestKeysAndSeriesChangeOnlyAsDesigned(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	admin, api := dbtest.AdminPool(t), dbtest.APIPool(t)
	ten := newTenant(t, admin, "DE")

	var key uuid.UUID
	mustInTenant(t, api, ten, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "insert into app.api_keys (tenant_id, name, prefix, secret_hash) values ($1, 'k', $2, $3) returning id",
			ten.ID, randomHex(t, 6), make([]byte, 32)).Scan(&key)
	})

	for _, c := range []struct {
		name, sql, code string
		id              uuid.UUID
	}{
		{"replace a key's hash", "update app.api_keys set secret_hash = $1 where id = $2", errPrivilege, key},
		{"change a key's prefix", "update app.api_keys set prefix = 'other' where id = $1", errPrivilege, key},
		{"move the counter", "update app.number_series set next_value = next_value + 5 where id = $1", errImmutable, ten.SeriesID},
		{"rewind the counter", "update app.number_series set next_value = 1 where id = $1 and next_value > 1", errImmutable, ten.SeriesID},
		{"change a series prefix", "update app.number_series set prefix = 'X-' where id = $1", errPrivilege, ten.SeriesID},
	} {
		t.Run(c.name, func(t *testing.T) {
			args := []any{c.id}
			if c.name == "replace a key's hash" {
				args = []any{make([]byte, 32), c.id}
			}
			if c.name == "rewind the counter" {
				// Move the counter once, legally, so there is something to rewind.
				mustInTenant(t, api, ten, exec(t, "update app.number_series set next_value = next_value + 1 where id = $1", ten.SeriesID))
			}
			err := inTenant(t, api, ten, exec(t, c.sql, args...))
			wantSQLState(t, err, c.code)
		})
	}

	t.Run("revocation is final, even for postgres", func(t *testing.T) {
		revoke := "update app.api_keys set revoked_at = now() where id = $1"
		mustInTenant(t, api, ten, exec(t, revoke, key))
		wantSQLState(t, inTenant(t, api, ten, exec(t, "update app.api_keys set revoked_at = null where id = $1", key)), errImmutable)
		_, err := admin.Exec(ctx, "update app.api_keys set revoked_at = null where id = $1", key)
		wantSQLState(t, err, errImmutable)
	})
}
