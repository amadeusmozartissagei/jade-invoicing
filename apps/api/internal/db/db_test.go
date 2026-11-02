package db

import (
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testURL returns the database for tests: the local Supabase stack
// (`supabase db start`) or the service in CI. Without TEST_DATABASE_URL the
// test is skipped locally but fails in CI, so a broken setup cannot turn the
// database tests into silent skips.
func testURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL is not set in CI")
		}
		t.Skip("TEST_DATABASE_URL not set; start the local stack with `supabase db start`")
	}
	return url
}

// openTestPool opens a pool on the test database, closed when the test ends.
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := Open(t.Context(), testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// The migrations in supabase/migrations must have run: the extensions that
// background jobs rely on (ADR 0003) are installed.
func TestMigrationsApplied(t *testing.T) {
	ctx := t.Context()
	pool := openTestPool(t)

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

// Exec mode sends parameters as text and infers their types from Go values;
// numeric and timestamptz round trips must stay exact (ADR 0005).
func TestExecModeRoundTrip(t *testing.T) {
	ctx := t.Context()
	pool := openTestPool(t)

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
