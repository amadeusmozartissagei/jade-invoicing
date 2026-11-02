// Package db opens the Postgres connection pool and runs tenant-scoped
// transactions (ADR 0005, ADR 0007).
package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxConns is kept small because every server instance gets its own pool and
// all of them share the Supabase transaction pooler. Revisit with the
// pooler's client limit before the first load test.
const maxConns = 4

// Config parses url into the pool configuration Open uses. Queries use
// QueryExecModeExec: parameters are sent separately from the SQL text without
// named prepared statements, which the transaction pooler does not support.
func Config(url string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	return cfg, nil
}

// New creates a pool for url without connecting: connections open on first
// use, so the server starts, and /healthz answers, while the database is
// paused or unreachable.
func New(url string) (*pgxpool.Pool, error) {
	cfg, err := Config(url)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	return pool, nil
}

// Open connects to url and checks the connection.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := Config(url)
	if err != nil {
		return nil, err
	}
	return OpenConfig(ctx, cfg)
}

// OpenConfig connects with cfg, usually from Config, and checks the connection.
func OpenConfig(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// InTenantTx runs fn in one transaction scoped to tenantID, and commits if fn
// returns nil. The tenant is set with set_config(..., true), which lasts only
// until the transaction ends, so it is safe behind the transaction pooler; the
// RLS policies read it through app.current_tenant_id(). Every query on tenant
// data goes through here.
func InTenantTx(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "select set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
			return fmt.Errorf("set tenant: %w", err)
		}
		return fn(tx)
	})
}
