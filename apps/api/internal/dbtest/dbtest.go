// Package dbtest connects tests to the local Supabase Postgres
// (`supabase db start`) or the one in CI.
package dbtest

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/db"
)

// URL returns TEST_DATABASE_URL. Without it the test is skipped locally but
// fails in CI, so a broken setup cannot turn the database tests into silent
// skips. The URL must connect as postgres, as the local stack and CI do.
func URL(t *testing.T) string {
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

// AdminPool connects as postgres, which owns the schema and bypasses RLS.
// Tests use it for setup that the API cannot do (creating tenants) and to
// check what the API's role cannot see.
func AdminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := db.Config(URL(t))
	if err != nil {
		t.Fatal(err)
	}
	return open(t, cfg)
}

// APIPool returns a pool whose connections run as jade_api, the role the API
// logs in as: no BYPASSRLS, only its own grants. The tests have no password
// for jade_api, so each connection logs in as postgres and switches with SET
// ROLE. That is session state, so it needs a direct connection: behind a
// transaction pooler a later transaction can land on a server connection
// that never ran it. The pool is larger than the API's, for the concurrency
// tests.
func APIPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := db.Config(URL(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 16
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "set role jade_api")
		return err
	}
	return open(t, cfg)
}

func open(t *testing.T, cfg *pgxpool.Config) *pgxpool.Pool {
	t.Helper()
	pool, err := db.OpenConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
