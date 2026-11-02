// Package db opens the Postgres connection pool (ADR 0005).
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxConns is kept small because every server instance gets its own pool and
// all of them share the Supabase transaction pooler. Revisit with the
// pooler's client limit before the first load test.
const maxConns = 4

// Open connects to url and checks the connection. Queries use
// QueryExecModeExec: parameters are sent separately from the SQL text without
// named prepared statements, which the transaction pooler does not support.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec

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
