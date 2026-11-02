package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/contracttest"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	return New(Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Commit: "abc123"})
}

func serve(h http.Handler, r *http.Request) *http.Response {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Result()
}

type problemBody struct {
	Type      string `json:"type"`
	Code      string `json:"code"`
	Status    int    `json:"status"`
	RequestID string `json:"request_id"`
}

func decodeProblem(t *testing.T, resp *http.Response) problemBody {
	t.Helper()
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var p problemBody
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.RequestID != resp.Header.Get("Request-Id") {
		t.Errorf("request_id %q does not match Request-Id header %q", p.RequestID, resp.Header.Get("Request-Id"))
	}
	return p
}

func TestHealth(t *testing.T) {
	resp := serve(newHandler(t), httptest.NewRequest("GET", "/healthz", nil))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["commit"] != "abc123" {
		t.Errorf("body = %v", body)
	}
	if id := resp.Header.Get("Request-Id"); !uuidV4.MatchString(id) {
		t.Errorf("Request-Id %q is not a UUID v4", id)
	}
}

func TestRequestIDIgnoresClientValue(t *testing.T) {
	r := httptest.NewRequest("GET", "/healthz", nil)
	r.Header.Set("Request-Id", "chosen-by-client")
	resp := serve(newHandler(t), r)

	if got := resp.Header.Get("Request-Id"); got == "chosen-by-client" || !uuidV4.MatchString(got) {
		t.Errorf("Request-Id = %q, want a new server-generated UUID", got)
	}
}

// An unknown /v1 resource gets the contract's not_found problem. The response
// is checked against the 404 that openapi.yaml declares for getCustomer, so a
// drift between this writer and the Problem schema fails here.
func TestUnmatchedRouteIsNotFoundProblem(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/customers/0b6c5e2e-1111-4222-8333-944455556666", nil)
	resp := serve(newHandler(t), r)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	contracttest.ValidateResponse(t, r, resp)
	p := decodeProblem(t, resp)
	if p.Code != "not_found" || p.Status != 404 || !strings.HasSuffix(p.Type, "#not_found") {
		t.Errorf("problem = %+v", p)
	}
}

func TestWrongMethodIsMethodNotAllowedProblem(t *testing.T) {
	resp := serve(newHandler(t), httptest.NewRequest("POST", "/healthz", nil))

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow = %q, want it to list GET", allow)
	}
	if p := decodeProblem(t, resp); p.Code != "method_not_allowed" {
		t.Errorf("code = %q", p.Code)
	}
}

func TestOversizedBodyIsRejected(t *testing.T) {
	body := bytes.Repeat([]byte("a"), MaxBodyBytes+1)
	resp := serve(newHandler(t), httptest.NewRequest("POST", "/v1/customers", bytes.NewReader(body)))

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	if p := decodeProblem(t, resp); p.Code != "content_too_large" {
		t.Errorf("code = %q", p.Code)
	}
}

func TestBodyWithoutLengthIsCutOff(t *testing.T) {
	var readErr error
	h := limitBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))
	r := httptest.NewRequest("POST", "/", bytes.NewReader(bytes.Repeat([]byte("a"), MaxBodyBytes+1)))
	r.ContentLength = -1 // unknown, as with chunked encoding
	serve(h, r)

	var maxErr *http.MaxBytesError
	if !errors.As(readErr, &maxErr) {
		t.Errorf("read error = %v, want *http.MaxBytesError", readErr)
	}
}

func TestPanicBecomesInternalErrorProblem(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	h := requestID(logRequests(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("secret detail")
	})))
	resp := serve(h, httptest.NewRequest("GET", "/", nil))

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if bytes.Contains(raw, []byte("secret detail")) {
		t.Error("panic value leaked into the response")
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	if p := decodeProblem(t, resp); p.Code != "internal_error" {
		t.Errorf("code = %q", p.Code)
	}
	if !strings.Contains(logs.String(), "secret detail") {
		t.Error("panic value missing from the log")
	}
}
