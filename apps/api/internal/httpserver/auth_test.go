package httpserver

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/apikey"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/contracttest"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/db"
)

var requestIDField = regexp.MustCompile(`"request_id":"[^"]*"`)

// unauthorizedShape is a 401 reduced to what must be the same for every
// rejected key: status, the headers a client sees, and the body without
// request_id.
func unauthorizedShape(t *testing.T, req *http.Request, resp *http.Response) string {
	t.Helper()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	contracttest.ValidateResponse(t, req, resp)
	body, _ := io.ReadAll(resp.Body)
	return strings.Join([]string{
		resp.Status,
		resp.Header.Get("Content-Type"),
		resp.Header.Get("WWW-Authenticate"),
		requestIDField.ReplaceAllString(string(body), `"request_id":""`),
	}, "\n")
}

// A malformed key is rejected before any query, so these run with a pool
// that points nowhere: a lookup would fail and turn the 401 into a 500.
func TestMalformedKeysNeverReachTheDatabase(t *testing.T) {
	pool, err := db.New("postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	h := New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: pool})

	k, _ := apikey.Generate()
	headers := map[string]string{
		"missing":          "",
		"basic scheme":     "Basic " + k.Full,
		"no token":         "Bearer",
		"empty token":      "Bearer ",
		"other prefix":     "Bearer jade_pk_" + k.Prefix + "_" + k.Secret,
		"truncated secret": "Bearer " + k.Full[:len(k.Full)-1],
		"uppercase key":    "Bearer " + strings.ToUpper(k.Full),
		"two keys":         "Bearer " + k.Full + " " + k.Full,
	}
	var first string
	for name, header := range headers {
		req := httptest.NewRequest("GET", "/v1/tenant", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		got := unauthorizedShape(t, req, serve(h, req))
		if first == "" {
			first = got
		} else if got != first {
			t.Errorf("%s: response differs:\n%s\nwant\n%s", name, got, first)
		}
	}
	if !strings.Contains(first, "\nBearer\n") {
		t.Errorf("WWW-Authenticate missing:\n%s", first)
	}
}

func TestBearerSchemeIsCaseInsensitive(t *testing.T) {
	k, _ := apikey.Generate()
	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		prefix, secret, ok := bearerKey(scheme + " " + k.Full)
		if !ok || prefix != k.Prefix || secret != k.Secret {
			t.Errorf("%s: not accepted", scheme)
		}
	}
}

func TestNoDatabaseConfigured(t *testing.T) {
	var logs bytes.Buffer
	h := New(Config{Logger: slog.New(slog.NewTextHandler(&logs, nil))})

	resp := serve(h, httptest.NewRequest("GET", "/v1/tenant", nil))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if p := decodeProblem(t, resp); p.Code != "internal_error" {
		t.Errorf("code = %q", p.Code)
	}
	if !strings.Contains(logs.String(), "database not configured") {
		t.Errorf("log = %s", logs.String())
	}
	if resp := serve(h, httptest.NewRequest("GET", "/healthz", nil)); resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz = %d without a database", resp.StatusCode)
	}
}

func TestBufferWriter(t *testing.T) {
	b := newBufferWriter()
	b.Header().Set("Content-Type", "application/json")
	b.WriteHeader(http.StatusCreated)
	b.WriteHeader(http.StatusTeapot) // ignored, as in net/http
	_, _ = b.Write([]byte(`{}`))

	rec := httptest.NewRecorder()
	rec.Header().Set("Request-Id", "req-1")
	if rec.Body.Len() != 0 {
		t.Fatal("buffer wrote through")
	}
	b.flushTo(rec)
	if rec.Code != http.StatusCreated || rec.Body.String() != `{}` ||
		rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("Request-Id") != "req-1" {
		t.Errorf("flushed %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
}
