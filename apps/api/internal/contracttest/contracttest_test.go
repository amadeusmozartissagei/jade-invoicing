package contracttest

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// recorder counts failures instead of failing the real test.
type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Errorf(string, ...any) { r.failed = true }
func (r *recorder) Fatalf(string, ...any) { r.failed = true }
func (r *recorder) Helper()               {}

func problemResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header: http.Header{
			"Content-Type": {"application/problem+json"},
			"Request-Id":   {"0b6c5e2e-1111-4222-8333-944455556666"},
		},
		Body: io.NopCloser(bytes.NewBufferString(body)),
	}
}

// The validator must reject a response that breaks the contract; otherwise
// every test using it would pass vacuously.
func TestValidateResponse(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/customers/0b6c5e2e-1111-4222-8333-944455556666", nil)
	cases := map[string]struct {
		body     string
		wantFail bool
	}{
		"valid problem":    {`{"type":"https://example.com/e#not_found","title":"Not found","status":404,"code":"not_found","request_id":"x"}`, false},
		"missing code":     {`{"type":"https://example.com/e#not_found","title":"Not found","status":404,"request_id":"x"}`, true},
		"status as string": {`{"type":"https://example.com/e#not_found","title":"Not found","status":"404","code":"not_found","request_id":"x"}`, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{TB: t}
			ValidateResponse(rec, req, problemResponse(c.body))
			if rec.failed != c.wantFail {
				t.Errorf("failed = %v, want %v", rec.failed, c.wantFail)
			}
		})
	}
}
