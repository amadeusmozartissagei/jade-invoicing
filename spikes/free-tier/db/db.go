// Package db holds one lazily created connection pool per function instance.
package db

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	once    sync.Once
	pool    *pgxpool.Pool
	initErr error
)

// Pool returns the shared pool, creating it on first use.
//
// The Supabase transaction pooler (port 6543) does not support prepared
// statements, so every query uses the simple protocol.
func Pool(ctx context.Context) (*pgxpool.Pool, error) {
	once.Do(func() {
		url := os.Getenv("DATABASE_URL")
		if url == "" {
			initErr = errors.New("DATABASE_URL is not set")
			return
		}
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			initErr = err
			return
		}
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		cfg.MaxConns = 4
		pool, initErr = pgxpool.NewWithConfig(ctx, cfg)
	})
	return pool, initErr
}
