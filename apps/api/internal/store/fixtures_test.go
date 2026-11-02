package store_test

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/db"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/store"
)

// SQLSTATEs raised by the triggers (supabase/migrations/*_app_invariants.sql).
const (
	errImmutable  = "JD001"
	errNotPayable = "JD002"
	errMinorUnits = "JD003"
	errCheck      = "23514"
	errForeignKey = "23503"
	errUnique     = "23505"
	errPrivilege  = "42501" // also a row that fails an RLS policy
)

var issueDay = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

// tenant is a synthetic tenant created for one test, so tests can run in
// parallel on a shared database without seeing each other's rows.
type tenant struct {
	ID           uuid.UUID
	SeriesID     uuid.UUID // the default series, created by a trigger
	Jurisdiction string
	Currency     string
	Rounding     string // recorded on issued invoices
	TaxRate      string
	TaxOn100     string // tax at TaxRate on a net amount of 100
}

// newTenant creates a tenant as postgres, as the admin script will in phase 1.
func newTenant(t *testing.T, admin *pgxpool.Pool, jurisdiction string) tenant {
	t.Helper()
	ctx := t.Context()
	ten := tenant{Jurisdiction: "DE", Currency: "EUR", Rounding: "half_away_from_zero", TaxRate: "19", TaxOn100: "19.00"}
	timezone, jpRounding := "Europe/Berlin", (*string)(nil)
	if jurisdiction == "JP" {
		ten = tenant{Jurisdiction: "JP", Currency: "JPY", Rounding: "half_up", TaxRate: "10", TaxOn100: "10"}
		timezone, jpRounding = "Asia/Tokyo", &ten.Rounding
	}
	err := admin.QueryRow(ctx, `
		insert into app.tenants (name, jurisdiction, currency, timezone, jp_tax_rounding, fiscal_year_end_month)
		values ($1, $2, $3, $4, $5, 3)
		returning id`,
		"Test tenant "+t.Name(), jurisdiction, ten.Currency, timezone, jpRounding).Scan(&ten.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = admin.QueryRow(ctx, "select id from app.number_series where tenant_id = $1 and code = 'default'", ten.ID).Scan(&ten.SeriesID)
	if err != nil {
		t.Fatal(err)
	}
	return ten
}

// inTenant runs fn as the API would: one transaction scoped to the tenant.
func inTenant(t *testing.T, api *pgxpool.Pool, ten tenant, fn func(pgx.Tx) error) error {
	t.Helper()
	return db.InTenantTx(t.Context(), api, ten.ID, fn)
}

// mustInTenant is inTenant for setup steps that must succeed.
func mustInTenant(t *testing.T, api *pgxpool.Pool, ten tenant, fn func(pgx.Tx) error) {
	t.Helper()
	if err := inTenant(t, api, ten, fn); err != nil {
		t.Fatal(err)
	}
}

// exec returns a function that runs one statement, for inTenant.
func exec(t *testing.T, sql string, args ...any) func(pgx.Tx) error {
	return func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), sql, args...)
		return err
	}
}

func newCustomer(t *testing.T, tx pgx.Tx, ten tenant) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := tx.QueryRow(t.Context(), `
		insert into app.customers (tenant_id, kind, name, city, post_code, country_code)
		values ($1, 'business', 'Example Buyer Ltd', 'Example City', '00000', 'DE')
		returning id`, ten.ID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// newDraft creates a draft with one line of 100 and matching totals. preceding
// is required for credit notes and corrected invoices.
func newDraft(t *testing.T, tx pgx.Tx, ten tenant, customer uuid.UUID, documentType string, preceding *uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		insert into app.invoices (tenant_id, document_type, customer_id, series_id, preceding_invoice_id,
		  jurisdiction, currency, supply_date,
		  line_net_total, tax_exclusive_total, tax_total, tax_inclusive_total, amount_due)
		values ($1, $2, $3, $4, $5, $6, $7, $8, 100, 100, $9, 100 + $9::numeric, 100 + $9::numeric)
		returning id`,
		ten.ID, documentType, customer, ten.SeriesID, preceding, ten.Jurisdiction, ten.Currency, issueDay, ten.TaxOn100).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		insert into app.invoice_lines (tenant_id, invoice_id, position, name, quantity, unit_price, tax_rate, line_net_amount)
		values ($1, $2, 1, 'Consulting', 1, 100, $3, 100)`, ten.ID, id, ten.TaxRate)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
		insert into app.invoice_tax_breakdowns (tenant_id, invoice_id, tax_category, tax_rate, taxable_amount, tax_amount)
		values ($1, $2, 'S', $3, 100, $4)`, ten.ID, id, ten.TaxRate, ten.TaxOn100)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// issue runs steps 1, 2, and 4 of the issue transaction with the generated
// queries and returns the assigned number.
func issue(t *testing.T, tx pgx.Tx, ten tenant, id uuid.UUID) (string, error) {
	ctx := t.Context()
	q := store.New(tx)
	inv, err := q.LockInvoice(ctx, id)
	if err != nil {
		return "", err
	}
	next, err := q.TakeNextNumber(ctx, inv.SeriesID)
	if err != nil {
		return "", err
	}
	n, err := q.MarkInvoiceIssued(ctx, store.MarkInvoiceIssuedParams{
		ID:             id,
		SequenceValue:  next.SequenceValue,
		Number:         next.Number,
		IssueDate:      issueDay,
		RetentionUntil: issueDay.AddDate(8, 0, 0),
		TaxRounding:    ten.Rounding,
	})
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", errors.New("invoice is not a draft")
	}
	return next.Number, nil
}

func mustIssue(t *testing.T, tx pgx.Tx, ten tenant, id uuid.UUID) {
	t.Helper()
	if _, err := issue(t, tx, ten, id); err != nil {
		t.Fatal(err)
	}
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// wantSQLState fails the test unless err is a Postgres error with code.
func wantSQLState(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error = %v, want SQLSTATE %s", err, code)
	}
	if pgErr.Code != code {
		t.Fatalf("SQLSTATE %s (%s), want %s", pgErr.Code, pgErr.Message, code)
	}
}

// wantConstraint is wantSQLState that also checks which constraint failed.
func wantConstraint(t *testing.T, err error, code, constraint string) {
	t.Helper()
	wantSQLState(t, err, code)
	var pgErr *pgconn.PgError
	errors.As(err, &pgErr)
	if pgErr.ConstraintName != constraint {
		t.Fatalf("constraint %q (%s), want %q", pgErr.ConstraintName, pgErr.Message, constraint)
	}
}
