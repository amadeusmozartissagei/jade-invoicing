package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/db"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/dbtest"
)

func readyBody(t *testing.T, resp *http.Response) map[string]string {
	t.Helper()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// The keep-alive depends on /readyz really querying the database (ADR 0013).
func TestReadyQueriesDatabase(t *testing.T) {
	var logs bytes.Buffer
	h := New(Config{Logger: slog.New(slog.NewTextHandler(&logs, nil)), DB: dbtest.APIPool(t)})
	resp := serve(h, httptest.NewRequest("GET", "/readyz", nil))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; log:\n%s", resp.StatusCode, logs.String())
	}
	if body := readyBody(t, resp); body["status"] != "ok" || body["database"] != "ok" {
		t.Errorf("body = %v", body)
	}
}

func TestReadyWithoutDatabaseIsUnavailable(t *testing.T) {
	var logs bytes.Buffer
	h := New(Config{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	resp := serve(h, httptest.NewRequest("GET", "/readyz", nil))

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if body := readyBody(t, resp); body["status"] != "unavailable" {
		t.Errorf("body = %v", body)
	}
	if !strings.Contains(logs.String(), "database not configured") {
		t.Errorf("log does not name the cause:\n%s", logs.String())
	}
}

// A database that stops answering turns /readyz into a 503, and the error
// stays in the log, out of the response.
func TestReadyWithUnreachableDatabaseIsUnavailable(t *testing.T) {
	pool := dbtest.APIPool(t)
	pool.Close()
	var logs bytes.Buffer
	h := New(Config{Logger: slog.New(slog.NewTextHandler(&logs, nil)), DB: pool})
	resp := serve(h, httptest.NewRequest("GET", "/readyz", nil))

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if body := readyBody(t, resp); len(body) != 2 || body["database"] != "unavailable" {
		t.Errorf("body = %v, want only status and database", body)
	}
	if !strings.Contains(logs.String(), "readiness") {
		t.Errorf("log has no readiness entry:\n%s", logs.String())
	}
}

// A database that accepts connections but never answers must not hold the
// request: /readyz gives up at readyTimeout and answers 503.
func TestReadyWithHangingDatabaseTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	go func() {
		for {
			conn, err := ln.Accept() // accept, never reply
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	pool, err := db.New("postgresql://jade_api:x@" + ln.Addr().String() + "/postgres?sslmode=disable&connect_timeout=30")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	old := readyTimeout
	readyTimeout = 200 * time.Millisecond
	t.Cleanup(func() { readyTimeout = old })

	h := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: pool})
	start := time.Now()
	resp := serve(h, httptest.NewRequest("GET", "/readyz", nil))

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v, want about readyTimeout", d)
	}
}
