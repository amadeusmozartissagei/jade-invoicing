package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/api"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/problem"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/reqvalidate"
)

// contract is the embedded spec's validator, shared by every handler New
// builds. Parsing takes about 170 ms, so New starts it in the background:
// the server answers /healthz at once, and on a cold start the parse overlaps
// the wait for the first /v1 request instead of adding to it.
var contract struct {
	once sync.Once
	v    *reqvalidate.Validator
	err  error
}

func loadContract() (*reqvalidate.Validator, error) {
	contract.once.Do(func() { contract.v, contract.err = reqvalidate.New(api.Spec) })
	return contract.v, contract.err
}

func warmContract() { go func() { _, _ = loadContract() }() }

// validateRequests checks a request against the contract before the handler
// runs (ADR 0010, ADR 0012). It sits behind authenticate, so a client without
// a valid key learns nothing about the request format.
//
//   - A body with the wrong Content-Type gets 415, a body that is not JSON 400.
//   - A bad path or query parameter gets 400 bad_request.
//   - A body that breaks the schema gets 422 validation_failed, with one
//     entry in errors per field.
func validateRequests(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := RequestID(r.Context())
			// r.Pattern is the route, "PUT /v1/seller-profile"; the contract
			// writes the same path without the /v1 server prefix.
			method, path, _ := strings.Cut(r.Pattern, " ")
			specPath := strings.TrimPrefix(path, "/v1")

			var body []byte
			if hasBody(method) {
				var ok bool
				if body, ok = readBody(w, r); !ok {
					return
				}
			}

			v, err := loadContract()
			if err != nil {
				log.Error("load request validator", "request_id", id, "error", err)
				problem.Write(w, problem.New(problem.Internal, id, ""))
				return
			}
			out, err := v.Check(r, method, specPath, body)
			if err != nil {
				log.Error("validate request", "request_id", id, "error", err)
				problem.Write(w, problem.New(problem.Internal, id, ""))
				return
			}
			if len(out.Params) > 0 {
				problem.Write(w, problem.New(problem.BadRequest, id, strings.Join(out.Params, "; ")))
				return
			}
			if len(out.Fields) > 0 {
				p := problem.New(problem.ValidationFailed, id, fieldCount(len(out.Fields)))
				p.Errors = &out.Fields
				problem.Write(w, p)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func hasBody(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch
}

// readBody checks the Content-Type, reads the body, and checks that it is
// JSON, answering 415, 413, or 400 itself when not; ok is false then. r.Body
// is replaced with a reader over the same bytes, so the body can be read
// again. The idempotency middleware and validateRequests both use it, so a
// request is replayed only if it would also pass these checks.
func readBody(w http.ResponseWriter, r *http.Request) (body []byte, ok bool) {
	id := RequestID(r.Context())
	want := "application/json"
	if r.Method == http.MethodPatch {
		want = "application/merge-patch+json"
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != want {
		problem.Write(w, problem.New(problem.UnsupportedMediaType, id, "Send the body as "+want+"."))
		return nil, false
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			problem.Write(w, problem.New(problem.ContentTooLarge, id,
				fmt.Sprintf("Request bodies are limited to %d bytes.", MaxBodyBytes)))
			return nil, false
		}
		problem.Write(w, problem.New(problem.BadRequest, id, "The request body could not be read."))
		return nil, false
	}
	if !json.Valid(body) {
		problem.Write(w, problem.New(problem.BadRequest, id, "The request body is not valid JSON."))
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, true
}

func fieldCount(n int) string {
	if n == 1 {
		return "1 field is invalid."
	}
	return fmt.Sprintf("%d fields are invalid.", n)
}
