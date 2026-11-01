package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/amadeusmozartissagei/jade-invoicing/spikes/free-tier/db"
)

var (
	instanceStarted = time.Now()
	invocations     atomic.Int64
)

// Health measures pool setup and one round trip to Postgres, and reports
// whether this request hit a fresh (cold) function instance.
func Health(w http.ResponseWriter, r *http.Request) {
	if !db.Authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	n := invocations.Add(1)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	t0 := time.Now()
	pool, err := db.Pool(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	poolMs := time.Since(t0).Milliseconds()

	var version string
	t1 := time.Now()
	if err := pool.QueryRow(ctx, "select version()").Scan(&version); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	queryMs := time.Since(t1).Milliseconds()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"cold":            n == 1,
		"invocation":      n,
		"instance_age_ms": time.Since(instanceStarted).Milliseconds(),
		"pool_init_ms":    poolMs,
		"query_ms":        queryMs,
		"region":          os.Getenv("VERCEL_REGION"),
		"postgres":        version,
	})
}
