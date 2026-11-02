package db_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/db"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/dbtest"
)

// The migrations in supabase/migrations must have run: the extensions that
// background jobs rely on (ADR 0003) are installed.
func TestMigrationsApplied(t *testing.T) {
	ctx := t.Context()
	pool := dbtest.AdminPool(t)

	for _, ext := range []string{"pg_cron", "pg_net", "pgmq"} {
		var installed bool
		err := pool.QueryRow(ctx, "select exists (select 1 from pg_extension where extname = $1)", ext).Scan(&installed)
		if err != nil {
			t.Fatal(err)
		}
		if !installed {
			t.Errorf("extension %s is not installed", ext)
		}
	}
}

// Exec mode sends parameters as text without type OIDs, so the server infers
// each type from the SQL (ADR 0011);
// numeric and timestamptz round trips must stay exact (ADR 0005).
func TestExecModeRoundTrip(t *testing.T) {
	ctx := t.Context()
	pool := dbtest.AdminPool(t)

	var sum string
	if err := pool.QueryRow(ctx, "select ($1::numeric + $2::numeric)::text", "0.10", "0.20").Scan(&sum); err != nil {
		t.Fatal(err)
	}
	if sum != "0.30" {
		t.Errorf("0.10 + 0.20 = %s, want 0.30", sum)
	}

	want := time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)
	var got time.Time
	if err := pool.QueryRow(ctx, "select $1::timestamptz", want).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Errorf("timestamptz round trip = %v, want %v", got, want)
	}
}

// The generated queries map numeric to decimal.Decimal and date to
// time.Time. Both must survive exec mode exactly, without a registered codec.
func TestDecimalAndDateRoundTrip(t *testing.T) {
	ctx := t.Context()
	pool := dbtest.AdminPool(t)

	for _, s := range []string{"0.10", "1234567890123456.78", "-0.005", "123.456789"} {
		in := decimal.RequireFromString(s)
		var out decimal.Decimal
		if err := pool.QueryRow(ctx, "select $1::numeric", in).Scan(&out); err != nil {
			t.Fatal(err)
		}
		if !out.Equal(in) || out.String() != in.String() {
			t.Errorf("numeric round trip of %s = %s", in, out)
		}
	}

	// A date is midnight UTC in Go, and must not shift in a session whose time
	// zone is ahead of or behind UTC.
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	for _, tz := range []string{"Asia/Tokyo", "America/Los_Angeles"} {
		var got time.Time
		err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "select set_config('timezone', $1, true)", tz); err != nil {
				return err
			}
			return tx.QueryRow(ctx, "select $1::date", day).Scan(&got)
		})
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(day) {
			t.Errorf("date round trip in %s = %v, want %v", tz, got, day)
		}
	}
}

// Round is half away from zero, the rule for line amounts and German VAT
// (data-model.md, "Tax calculation"). RoundUp and RoundDown are away from and
// toward zero, two of the Japanese modes; amounts in phase 1 are never
// negative, so they equal ceiling and floor there.
func TestDecimalRounding(t *testing.T) {
	cases := []struct {
		in              string
		places          int32
		round, up, down string
	}{
		{"0.125", 2, "0.13", "0.13", "0.12"},
		{"-0.125", 2, "-0.13", "-0.13", "-0.12"},
		{"0.135", 2, "0.14", "0.14", "0.13"},
		{"2.5", 0, "3", "3", "2"},
		{"-2.5", 0, "-3", "-3", "-2"},
		{"2.4", 0, "2", "3", "2"},
		{"98765.36", 0, "98765", "98766", "98765"},
	}
	for _, c := range cases {
		d := decimal.RequireFromString(c.in)
		if got := d.Round(c.places).String(); got != c.round {
			t.Errorf("Round(%s, %d) = %s, want %s", c.in, c.places, got, c.round)
		}
		if got := d.RoundUp(c.places).String(); got != c.up {
			t.Errorf("RoundUp(%s, %d) = %s, want %s", c.in, c.places, got, c.up)
		}
		if got := d.RoundDown(c.places).String(); got != c.down {
			t.Errorf("RoundDown(%s, %d) = %s, want %s", c.in, c.places, got, c.down)
		}
	}
}

// The tenant set by InTenantTx is visible inside the transaction and gone
// after it, so a pooled connection never carries it to the next request.
func TestInTenantTxScopesTenant(t *testing.T) {
	ctx := t.Context()
	pool := dbtest.APIPool(t)
	tenant := uuid.New()

	var inside string
	err := db.InTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select app.current_tenant_id()::text").Scan(&inside)
	})
	if err != nil {
		t.Fatal(err)
	}
	if inside != tenant.String() {
		t.Errorf("tenant inside the transaction = %q, want %s", inside, tenant)
	}

	// The pool has opened only one connection, so this is the one used above.
	if n := pool.Stat().TotalConns(); n != 1 {
		t.Fatalf("pool has %d connections, want 1", n)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var after *string
	if err := conn.QueryRow(ctx, "select app.current_tenant_id()::text").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != nil {
		t.Errorf("tenant after commit = %q, want null", *after)
	}
}
