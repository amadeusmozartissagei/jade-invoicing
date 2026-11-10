package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/admin"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/contracttest"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/dbtest"
)

func TestParseIdempotencyKey(t *testing.T) {
	long := strings.Repeat("k", maxIdempotencyKey)
	cases := []struct {
		in, want string
		ok       bool
	}{
		{`"8e03978e-40d5-43e8-bc93-6894a57f9324"`, "8e03978e-40d5-43e8-bc93-6894a57f9324", true},
		{`8e03978e-40d5-43e8-bc93-6894a57f9324`, "8e03978e-40d5-43e8-bc93-6894a57f9324", true},
		{`"a\"b\\c"`, `a"b\c`, true},
		{`"` + long + `"`, long, true},
		{long, long, true},
		{`""`, "", false},
		{``, "", false},
		{`"` + long + `k"`, "", false},
		{long + "k", "", false},
		{`"abc`, "", false},
		{`"a"b"`, "", false},
		{`"a\x"`, "", false},
		{"café", "", false},
		{"a\tb", "", false},
	}
	for _, tc := range cases {
		got, ok := parseIdempotencyKey(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseIdempotencyKey(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// The parts are length-prefixed, so moving bytes between path and body
// changes the fingerprint.
func TestFingerprintSeparatesParts(t *testing.T) {
	a := fingerprint("POST", "/v1/a", []byte("b"))
	b := fingerprint("POST", "/v1/", []byte("ab"))
	c := fingerprint("PUT", "/v1/a", []byte("b"))
	if a == b || a == c {
		t.Error("different requests share a fingerprint")
	}
	if a != fingerprint("POST", "/v1/a", []byte("b")) {
		t.Error("fingerprint is not deterministic")
	}
}

// send is do with headers: Content-Type defaults to the method's (JSON, or
// merge patch for PATCH), and a "" value removes a header.
func (s dbServer) send(t *testing.T, key, method, path, body string, headers map[string]string) (*http.Request, *http.Response) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer "+key)
	switch method {
	case http.MethodPost:
		req.Header.Set("Content-Type", "application/json")
	case http.MethodPatch:
		req.Header.Set("Content-Type", "application/merge-patch+json")
	}
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	return req, serve(s.h, req)
}

func (s dbServer) createCustomer(t *testing.T, key, idemKey, body string) (*http.Request, *http.Response) {
	t.Helper()
	return s.send(t, key, "POST", "/v1/customers", body, map[string]string{"Idempotency-Key": idemKey})
}

func (s dbServer) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := s.admin.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (s dbServer) customers(t *testing.T, c admin.Created) int {
	t.Helper()
	return s.count(t, `select count(*) from app.customers where tenant_id = $1`, c.TenantID)
}

func (s dbServer) keys(t *testing.T, c admin.Created) int {
	t.Helper()
	return s.count(t, `select count(*) from app.idempotency_keys where tenant_id = $1`, c.TenantID)
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return b
}

func wantProblem(t *testing.T, req *http.Request, resp *http.Response, status int, code string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, status, readAll(t, resp))
	}
	contracttest.ValidateResponse(t, req, resp)
	if p := decodeProblem(t, resp); p.Code != code {
		t.Fatalf("code = %q, want %q", p.Code, code)
	}
}

const customerJSON = `{"kind":"business","name":"Beispiel Handels GmbH","city":"Berlin","post_code":"10115","country_code":"DE"}`

// Criterion 2.
func TestPostNeedsAWellFormedIdempotencyKey(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")

	req, resp := s.send(t, c.Key, "POST", "/v1/customers", customerJSON, nil)
	wantProblem(t, req, resp, http.StatusBadRequest, "idempotency_key_required")

	for _, bad := range []string{`""`, strings.Repeat("k", 256), `"open`, "café"} {
		req, resp := s.createCustomer(t, c.Key, bad, customerJSON)
		wantProblem(t, req, resp, http.StatusBadRequest, "bad_request")
	}

	// Two header lines are two values, not one key.
	req = httptest.NewRequest("POST", "/v1/customers", strings.NewReader(customerJSON))
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Add("Idempotency-Key", "a")
	req.Header.Add("Idempotency-Key", "b")
	wantProblem(t, req, serve(s.h, req), http.StatusBadRequest, "bad_request")

	if n := s.customers(t, c); n != 0 {
		t.Errorf("%d customers created without a valid key", n)
	}
}

// Criterion 3, and the quoted and bare forms naming one key.
func TestRepeatedPostReplaysTheFirstResponse(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")

	req, first := s.createCustomer(t, c.Key, `"key-1"`, customerJSON)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d; %s; log: %s", first.StatusCode, readAll(t, first), s.logs)
	}
	contracttest.ValidateResponse(t, req, first)
	if first.Header.Get("Idempotent-Replayed") != "" {
		t.Error("first response is marked as replayed")
	}
	firstBody := readAll(t, first)

	req, again := s.createCustomer(t, c.Key, `key-1`, customerJSON)
	if again.StatusCode != http.StatusCreated {
		t.Fatalf("replay status = %d; %s", again.StatusCode, readAll(t, again))
	}
	contracttest.ValidateResponse(t, req, again)
	if got := readAll(t, again); !bytes.Equal(got, firstBody) {
		t.Errorf("replayed body =\n%s\nwant\n%s", got, firstBody)
	}
	for _, h := range []string{"Location", "Content-Type"} {
		if again.Header.Get(h) != first.Header.Get(h) || again.Header.Get(h) == "" {
			t.Errorf("%s = %q, first was %q", h, again.Header.Get(h), first.Header.Get(h))
		}
	}
	if again.Header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("Idempotent-Replayed = %q", again.Header.Get("Idempotent-Replayed"))
	}
	if again.Header.Get("Request-Id") == first.Header.Get("Request-Id") {
		t.Error("replay reused the first request's Request-Id")
	}
	if n := s.customers(t, c); n != 1 {
		t.Errorf("customers = %d, want 1", n)
	}
}

// Criterion 4.
func TestSameKeyDifferentBodyIsRejected(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	if _, resp := s.createCustomer(t, c.Key, "key-1", customerJSON); resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	other := strings.Replace(customerJSON, "Berlin", "Hamburg", 1)
	req, resp := s.createCustomer(t, c.Key, "key-1", other)
	wantProblem(t, req, resp, http.StatusUnprocessableEntity, "idempotency_key_reused")
	if n := s.customers(t, c); n != 1 {
		t.Errorf("customers = %d, want 1", n)
	}
}

// Criterion 5: concurrent repeats create one customer; each other request
// gets 409 with Retry-After, or the replay if it ran after the commit.
func TestConcurrentRepeatsCreateOne(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")

	const n = 10
	var wg sync.WaitGroup
	statuses := make([]*http.Response, n)
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			req := httptest.NewRequest("POST", "/v1/customers", strings.NewReader(customerJSON))
			req.Header.Set("Authorization", "Bearer "+c.Key)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "same")
			statuses[i] = serve(s.h, req)
		})
	}
	close(start)
	wg.Wait()

	fresh := 0
	for _, resp := range statuses {
		switch {
		case resp.StatusCode == http.StatusCreated && resp.Header.Get("Idempotent-Replayed") == "":
			fresh++
		case resp.StatusCode == http.StatusCreated:
		case resp.StatusCode == http.StatusConflict:
			if resp.Header.Get("Retry-After") != "1" {
				t.Errorf("409 without Retry-After: 1")
			}
			if p := decodeProblem(t, resp); p.Code != "idempotency_key_in_use" {
				t.Errorf("409 code = %q", p.Code)
			}
		default:
			t.Errorf("status = %d; %s", resp.StatusCode, readAll(t, resp))
		}
	}
	if fresh != 1 {
		t.Errorf("%d requests created a customer, want 1", fresh)
	}
	if got := s.customers(t, c); got != 1 {
		t.Errorf("customers = %d, want 1", got)
	}
}

// Criterion 5, the 409 path made certain: another transaction holds the
// key's lock, as a request still running would.
func TestKeyInUseIsConflict(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")

	tx, err := s.admin.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(),
		`select pg_advisory_xact_lock(hashtextextended($1::text || ':' || $2::text, 0))`, c.TenantID.String(), "busy"); err != nil {
		t.Fatal(err)
	}

	req, resp := s.createCustomer(t, c.Key, "busy", customerJSON)
	if resp.Header.Get("Retry-After") != "1" {
		t.Errorf("Retry-After = %q", resp.Header.Get("Retry-After"))
	}
	wantProblem(t, req, resp, http.StatusConflict, "idempotency_key_in_use")

	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, resp := s.createCustomer(t, c.Key, "busy", customerJSON); resp.StatusCode != http.StatusCreated {
		t.Errorf("after the lock was released: status %d", resp.StatusCode)
	}
}

// Criterion 6: a failed POST stores nothing, so the same key works once the
// request is fixed.
func TestFailedPostDoesNotUseTheKey(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")

	req, resp := s.createCustomer(t, c.Key, "fix-me", `{"kind":"business"}`)
	wantProblem(t, req, resp, http.StatusUnprocessableEntity, "validation_failed")

	taken := `{"kind":"business","name":"Taken","external_ref":"cust-1"}`
	if _, resp := s.createCustomer(t, c.Key, "first", taken); resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	req, resp = s.createCustomer(t, c.Key, "dup", strings.Replace(taken, "Taken", "Again", 1))
	wantProblem(t, req, resp, http.StatusConflict, "conflict_external_ref")
	if n := s.keys(t, c); n != 1 {
		t.Errorf("idempotency rows = %d after two failures, want 1", n)
	}

	if _, resp := s.createCustomer(t, c.Key, "fix-me", customerJSON); resp.StatusCode != http.StatusCreated {
		t.Errorf("fixed request: status %d", resp.StatusCode)
	}
	if _, resp := s.createCustomer(t, c.Key, "dup", `{"kind":"business","name":"Again","external_ref":"cust-2"}`); resp.StatusCode != http.StatusCreated {
		t.Errorf("fixed duplicate: status %d", resp.StatusCode)
	}
}

// Criterion 7.
func TestKeysArePerTenant(t *testing.T) {
	s := newDBServer(t)
	a, b := s.tenant(t, "DE"), s.tenant(t, "DE")

	_, ra := s.createCustomer(t, a.Key, "shared", customerJSON)
	_, rb := s.createCustomer(t, b.Key, "shared", customerJSON)
	if ra.StatusCode != http.StatusCreated || rb.StatusCode != http.StatusCreated {
		t.Fatalf("statuses %d, %d", ra.StatusCode, rb.StatusCode)
	}
	if rb.Header.Get("Idempotent-Replayed") != "" {
		t.Error("tenant B got tenant A's stored response")
	}
	if s.customers(t, a) != 1 || s.customers(t, b) != 1 {
		t.Errorf("customers A=%d B=%d, want 1 each", s.customers(t, a), s.customers(t, b))
	}
}

// Criterion 8: an expired key is a new key, and the cleanup job deletes
// expired rows only.
func TestExpiredKeys(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")

	if _, resp := s.createCustomer(t, c.Key, "old", customerJSON); resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	s.exec(t, `update app.idempotency_keys set expires_at = now() - interval '1 second' where tenant_id = $1 and key = 'old'`, c.TenantID)

	// A different body with the expired key is a new request, not a reuse.
	_, resp := s.createCustomer(t, c.Key, "old", strings.Replace(customerJSON, "Berlin", "Hamburg", 1))
	if resp.StatusCode != http.StatusCreated || resp.Header.Get("Idempotent-Replayed") != "" {
		t.Fatalf("expired key: status %d, replayed %q", resp.StatusCode, resp.Header.Get("Idempotent-Replayed"))
	}
	if n := s.customers(t, c); n != 2 {
		t.Errorf("customers = %d, want 2", n)
	}

	if _, resp := s.createCustomer(t, c.Key, "stale", customerJSON); resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	s.exec(t, `update app.idempotency_keys set expires_at = now() - interval '1 second' where tenant_id = $1 and key = 'stale'`, c.TenantID)
	var deleted int64
	if err := s.admin.QueryRow(t.Context(), `select app.delete_expired_idempotency_keys()`).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted < 1 {
		t.Errorf("cleanup deleted %d rows", deleted)
	}
	left := s.count(t, `select count(*) from app.idempotency_keys where tenant_id = $1 and key in ('old', 'stale')`, c.TenantID)
	if left != 1 {
		t.Errorf("rows left = %d, want 1 (the fresh 'old')", left)
	}

	if n := s.count(t, `select count(*) from cron.job where jobname = 'jade-delete-expired-idempotency-keys' and schedule = '41 3 * * *'`); n != 1 {
		t.Errorf("cleanup cron jobs = %d, want 1", n)
	}
	// The API's role cannot run the cleanup across tenants.
	if _, err := dbtest.APIPool(t).Exec(t.Context(), `select app.delete_expired_idempotency_keys()`); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("jade_api ran the cleanup: %v", err)
	}
}

// Criterion 13: only POSTs write idempotency rows.
func TestReadsAndPatchesStoreNoKey(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	_, resp := s.createCustomer(t, c.Key, "k", customerJSON)
	var created struct{ ID string }
	decodeJSON(t, resp, &created)

	path := "/v1/customers/" + created.ID
	s.send(t, c.Key, "GET", path, "", nil)
	s.send(t, c.Key, "GET", "/v1/customers", "", nil)
	if _, resp := s.send(t, c.Key, "PATCH", path, `{"city":"Hamburg"}`, map[string]string{"Idempotency-Key": "ignored"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status = %d", resp.StatusCode)
	}
	if n := s.keys(t, c); n != 1 {
		t.Errorf("idempotency rows = %d, want 1", n)
	}
}

// A stored key does not bypass the body checks: the same bytes with the
// wrong Content-Type get 415, as they would without a stored key.
func TestReplayKeepsBodyChecks(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	if _, resp := s.createCustomer(t, c.Key, "k", customerJSON); resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	req, resp := s.send(t, c.Key, "POST", "/v1/customers", customerJSON,
		map[string]string{"Idempotency-Key": "k", "Content-Type": "text/plain"})
	wantProblem(t, req, resp, http.StatusUnsupportedMediaType, "unsupported_media_type")
}

// Recording a key's use never waits for another request of the same key: if
// it did, a repeated Idempotency-Key would queue behind the first request
// instead of getting 409 at once.
func TestKeyTouchDoesNotWait(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")

	tx, err := s.admin.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `select 1 from app.api_keys where id = $1 for update`, c.APIKeyID); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, resp := s.send(t, c.Key, "GET", "/v1/tenant", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; log: %s", resp.StatusCode, s.logs)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("request waited %v for the key row", d)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if at := s.lastUsed(t, c); at != nil {
		t.Errorf("last_used_at = %v; the touch should have been skipped", at)
	}
	// Once the row is free, the touch happens again.
	s.send(t, c.Key, "GET", "/v1/tenant", "", nil)
	if s.lastUsed(t, c) == nil {
		t.Error("last_used_at not set after the lock was released")
	}
}
