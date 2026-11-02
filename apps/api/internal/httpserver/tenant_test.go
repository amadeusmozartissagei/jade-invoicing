package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/admin"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/apikey"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/contracttest"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/dbtest"
)

// dbServer is the API as deployed: the pool runs as jade_api, under RLS. The
// admin pool (postgres) sets up tenants and changes what the API cannot.
type dbServer struct {
	h     http.Handler
	admin *pgxpool.Pool
	logs  *bytes.Buffer
}

func newDBServer(t *testing.T) dbServer {
	t.Helper()
	logs := &bytes.Buffer{}
	return dbServer{
		h:     New(Config{Logger: slog.New(slog.NewTextHandler(logs, nil)), DB: dbtest.APIPool(t)}),
		admin: dbtest.AdminPool(t),
		logs:  logs,
	}
}

func (s dbServer) tenant(t *testing.T, jurisdiction string) admin.Created {
	t.Helper()
	in := admin.TenantInput{Name: "Test " + t.Name(), Jurisdiction: jurisdiction, Timezone: "Europe/Berlin"}
	if jurisdiction == "JP" {
		in.Timezone, in.JPTaxRounding = "Asia/Tokyo", "down"
	}
	c, err := admin.CreateTenant(t.Context(), s.admin, in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (s dbServer) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.admin.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func (s dbServer) do(t *testing.T, key, method, path, body string) (*http.Request, *http.Response) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, serve(s.h, req)
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestGetTenant(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "JP")

	req, resp := s.do(t, c.Key, "GET", "/v1/tenant", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; log: %s", resp.StatusCode, s.logs)
	}
	contracttest.ValidateResponse(t, req, resp)
	var got map[string]any
	decodeJSON(t, resp, &got)
	if got["id"] != c.TenantID.String() || got["currency"] != "JPY" || got["jp_tax_rounding"] != "down" ||
		got["timezone"] != "Asia/Tokyo" || got["status"] != "active" {
		t.Errorf("tenant = %v", got)
	}
	if resp.Header.Get("Request-Id") == "" {
		t.Error("no Request-Id")
	}
	if at, _ := got["created_at"].(string); !strings.HasSuffix(at, "Z") {
		t.Errorf("created_at = %q, want UTC", at)
	}
}

func TestDETenantHasNullRounding(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	req, resp := s.do(t, c.Key, "GET", "/v1/tenant", "")
	contracttest.ValidateResponse(t, req, resp)
	var got map[string]any
	decodeJSON(t, resp, &got)
	if v, ok := got["jp_tax_rounding"]; !ok || v != nil {
		t.Errorf("jp_tax_rounding = %v, %v; want present and null", v, ok)
	}
}

// Acceptance criterion 9 of docs/design/api.md: unknown, expired, and revoked
// keys get the same 401 apart from request_id, as do a wrong secret for a
// real prefix and a malformed key.
func TestRejectedKeysLookTheSame(t *testing.T) {
	s := newDBServer(t)
	expired, revoked, real := s.tenant(t, "DE"), s.tenant(t, "DE"), s.tenant(t, "DE")
	s.exec(t, `update app.api_keys set expires_at = now() - interval '1 second' where id = $1`, expired.APIKeyID)
	s.exec(t, `update app.api_keys set revoked_at = now() where id = $1`, revoked.APIKeyID)

	unknown, _ := apikey.Generate()
	prefix, _, _ := apikey.Parse(real.Key)
	other, _ := apikey.Generate()

	keys := map[string]string{
		"malformed":    "jade_sk_nope",
		"unknown":      unknown.Full,
		"wrong secret": apikey.Scheme + prefix + "_" + other.Secret,
		"expired":      expired.Key,
		"revoked":      revoked.Key,
	}
	var first string
	for name, key := range keys {
		req, resp := s.do(t, key, "GET", "/v1/tenant", "")
		got := unauthorizedShape(t, req, resp)
		if first == "" {
			first = got
		} else if got != first {
			t.Errorf("%s: response differs:\n%s\nwant\n%s", name, got, first)
		}
	}

	// The real key still works, so the rejections above were about the keys.
	if _, resp := s.do(t, real.Key, "GET", "/v1/tenant", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("valid key: status %d", resp.StatusCode)
	}
}

func TestSuspendedTenantIsForbidden(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	s.exec(t, `update app.tenants set status = 'suspended' where id = $1`, c.TenantID)

	req, resp := s.do(t, c.Key, "GET", "/v1/tenant", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	contracttest.ValidateResponse(t, req, resp)
	if p := decodeProblem(t, resp); p.Code != "forbidden" {
		t.Errorf("code = %q", p.Code)
	}
}

func (s dbServer) lastUsed(t *testing.T, c admin.Created) *time.Time {
	t.Helper()
	var at *time.Time
	if err := s.admin.QueryRow(t.Context(), `select last_used_at from app.api_keys where id = $1`, c.APIKeyID).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func TestKeyUseIsRecordedAtMostOncePerMinute(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")

	s.do(t, c.Key, "GET", "/v1/tenant", "")
	first := s.lastUsed(t, c)
	if first == nil {
		t.Fatal("last_used_at not set")
	}
	s.do(t, c.Key, "GET", "/v1/tenant", "")
	if again := s.lastUsed(t, c); !again.Equal(*first) {
		t.Errorf("last_used_at moved within a minute: %v -> %v", first, again)
	}

	s.exec(t, `update app.api_keys set last_used_at = now() - interval '2 minutes' where id = $1`, c.APIKeyID)
	s.do(t, c.Key, "GET", "/v1/tenant", "")
	if later := s.lastUsed(t, c); !later.After(*first) {
		t.Errorf("last_used_at not updated after two minutes: %v", later)
	}
}

// An error response rolls the whole transaction back, including the record
// of the key's use.
func TestErrorResponseRollsBack(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")

	if _, resp := s.do(t, c.Key, "PUT", "/v1/seller-profile", `{"legal_nam":"x"}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	if at := s.lastUsed(t, c); at != nil {
		t.Errorf("last_used_at = %v after a 422; the transaction should have rolled back", at)
	}
}

// If the commit fails, the handler's 200 is never sent. Run as postgres,
// which may end its own session; jade_api may not.
func TestFailedCommitIsInternalError(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	h := requestID(authenticate(log, s.admin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = tenantFrom(r.Context()).tx.Exec(r.Context(), "select pg_terminate_backend(pg_backend_pid())")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})))

	req := httptest.NewRequest("GET", "/v1/tenant", nil)
	req.Header.Set("Authorization", "Bearer "+c.Key)
	resp := serve(h, req)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if p := decodeProblem(t, resp); p.Code != "internal_error" {
		t.Errorf("code = %q", p.Code)
	}
	if !strings.Contains(logs.String(), "tenant transaction") {
		t.Errorf("log = %s", logs.String())
	}
}

func TestSellerProfileRoundTrip(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")

	req, resp := s.do(t, c.Key, "GET", "/v1/seller-profile", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d; log: %s", resp.StatusCode, s.logs)
	}
	contracttest.ValidateResponse(t, req, resp)
	var empty map[string]any
	decodeJSON(t, resp, &empty)
	if at, _ := empty["updated_at"].(string); !strings.HasSuffix(at, "Z") {
		t.Errorf("updated_at = %q, want UTC", at)
	}
	if v, ok := empty["legal_name"]; !ok || v != nil {
		t.Errorf("new profile legal_name = %v, %v; want null", v, ok)
	}

	put := `{"legal_name":"Beispiel GmbH","city":"Berlin","post_code":"10115","country_code":"DE","vat_id":"DE000000000","contact_email":"rechnung@example.com"}`
	req, resp = s.do(t, c.Key, "PUT", "/v1/seller-profile", put)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d; log: %s", resp.StatusCode, s.logs)
	}
	contracttest.ValidateResponse(t, req, resp)

	_, resp = s.do(t, c.Key, "GET", "/v1/seller-profile", "")
	var got map[string]any
	decodeJSON(t, resp, &got)
	for k, want := range map[string]any{"legal_name": "Beispiel GmbH", "city": "Berlin", "contact_email": "rechnung@example.com", "iban": nil} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}

	// PUT replaces: a field left out becomes null.
	_, resp = s.do(t, c.Key, "PUT", "/v1/seller-profile", `{"legal_name":"Beispiel GmbH"}`)
	decodeJSON(t, resp, &got)
	if got["city"] != nil {
		t.Errorf("city = %v after a PUT without it, want null", got["city"])
	}
}

func TestSellerProfileIsPerTenant(t *testing.T) {
	s := newDBServer(t)
	a, b := s.tenant(t, "DE"), s.tenant(t, "DE")

	if _, resp := s.do(t, a.Key, "PUT", "/v1/seller-profile", `{"legal_name":"Tenant A"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d", resp.StatusCode)
	}
	_, resp := s.do(t, b.Key, "GET", "/v1/seller-profile", "")
	var got map[string]any
	decodeJSON(t, resp, &got)
	if got["legal_name"] != nil {
		t.Errorf("tenant B sees legal_name %v", got["legal_name"])
	}
}

func TestSellerProfileValidation(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")

	// A typo, a wrong type, and a valid field: only the first two are
	// reported (the unevaluatedProperties false positive, ADR 0010).
	req, resp := s.do(t, c.Key, "PUT", "/v1/seller-profile", `{"legal_name":5,"city":"Berlin","cty":"Berlin"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	contracttest.ValidateResponse(t, req, resp)
	var p struct {
		Code   string `json:"code"`
		Errors []struct{ Pointer, Code string }
	}
	decodeJSON(t, resp, &p)
	want := []struct{ Pointer, Code string }{{"/cty", "unknown_field"}, {"/legal_name", "invalid_format"}}
	if p.Code != "validation_failed" || !reflect.DeepEqual(p.Errors, want) {
		t.Errorf("problem = %+v, want errors %v", p, want)
	}

	cases := []struct {
		name, contentType, body string
		status                  int
		code                    string
	}{
		{"not JSON", "application/json", `{"legal_name":`, http.StatusBadRequest, "bad_request"},
		{"empty body", "application/json", ``, http.StatusBadRequest, "bad_request"},
		{"wrong content type", "text/plain", `{}`, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"no content type", "", `{}`, http.StatusUnsupportedMediaType, "unsupported_media_type"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("PUT", "/v1/seller-profile", strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+c.Key)
		if tc.contentType != "" {
			req.Header.Set("Content-Type", tc.contentType)
		}
		resp := serve(s.h, req)
		if resp.StatusCode != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.name, resp.StatusCode, tc.status)
			continue
		}
		contracttest.ValidateResponse(t, req, resp)
		if got := decodeProblem(t, resp); got.Code != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.name, got.Code, tc.code)
		}
	}

	// Charset parameters are fine.
	req = httptest.NewRequest("PUT", "/v1/seller-profile", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if resp := serve(s.h, req); resp.StatusCode != http.StatusOK {
		t.Errorf("charset parameter: status %d", resp.StatusCode)
	}
}

// Validation runs after authentication: without a key, a bad body is a 401,
// and nothing about the body's format leaks.
func TestValidationNeedsAKey(t *testing.T) {
	s := newDBServer(t)
	req, resp := s.do(t, "", "PUT", "/v1/seller-profile", `{"legal_nam":"x"}`)
	unauthorizedShape(t, req, resp)
}
