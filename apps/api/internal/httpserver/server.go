// Package httpserver assembles the HTTP handler: routes, middleware, and the
// problem responses for requests that match no route.
package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/problem"
)

// Config is what the handler needs from its environment.
type Config struct {
	Logger *slog.Logger
	// Commit is the deployed Git commit, shown by /healthz. Empty locally.
	Commit string
	// DB is the pool for tenant requests, connected as jade_api. Nil when
	// JADE_API_DATABASE_URL is not set: /healthz still works, and /v1
	// answers 500 with "database not configured" in the log.
	DB *pgxpool.Pool
}

// New returns the API's root handler.
func New(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health(cfg.Commit))
	registerV1(mux, cfg.Logger, cfg.DB)

	h := problemsForUnmatched(mux)
	h = limitBody(h)
	h = logRequests(cfg.Logger)(h)
	h = requestID(h)
	return h
}

// health reports that the process is up. It does not touch the database, so
// it stays cheap and works while the database is paused or unreachable.
func health(commit string) http.HandlerFunc {
	body, _ := json.Marshal(map[string]string{"status": "ok", "commit": commit})
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}

// problemsForUnmatched replaces the plain-text 404 and 405 that ServeMux
// writes for unmatched requests with problem details. ServeMux reports an
// empty pattern exactly when it has no route for the request; it still sets
// the Allow header for a 405, which is kept.
func problemsForUnmatched(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(&unmatchedWriter{ResponseWriter: w, requestID: RequestID(r.Context())}, r)
	})
}

type unmatchedWriter struct {
	http.ResponseWriter
	requestID string
	written   bool
}

func (w *unmatchedWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	w.written = true
	kind := problem.NotFound
	if code == http.StatusMethodNotAllowed {
		kind = problem.MethodNotAllowed
	}
	problem.Write(w.ResponseWriter, problem.New(kind, w.requestID, ""))
}

func (w *unmatchedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Write drops ServeMux's plain-text body; WriteHeader already sent the
// problem.
func (w *unmatchedWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusNotFound)
	}
	return len(b), nil
}
