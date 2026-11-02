// Package contracttest checks real HTTP responses against openapi/openapi.yaml
// in tests, so the server cannot drift from the contract unnoticed.
//
// It uses libopenapi-validator (MIT), which reads OpenAPI 3.1 and JSON Schema
// 2020-12 keywords such as unevaluatedProperties (ADR 0010).
package contracttest

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
)

var (
	once    sync.Once
	v       validator.Validator
	loadErr error
)

// specPath finds openapi/openapi.yaml from this file's location, so tests in
// any package can use it.
func specPath() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "openapi", "openapi.yaml")
}

func load() (validator.Validator, error) {
	once.Do(func() {
		var spec []byte
		spec, loadErr = os.ReadFile(specPath())
		if loadErr != nil {
			return
		}
		var doc libopenapi.Document
		doc, loadErr = libopenapi.NewDocument(spec)
		if loadErr != nil {
			return
		}
		var errs []error
		v, errs = validator.NewValidator(doc)
		if len(errs) > 0 {
			loadErr = errs[0]
		}
	})
	return v, loadErr
}

// ValidateResponse fails the test if resp does not match what the contract
// declares for req's operation and resp's status code. Paths are matched
// with the /v1 prefix of the spec's server URL. The body is read and put
// back, so the caller can still decode it.
func ValidateResponse(t testing.TB, req *http.Request, resp *http.Response) {
	t.Helper()
	val, err := load()
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	check := *resp
	check.Body = io.NopCloser(bytes.NewReader(body))

	if ok, errs := val.ValidateHttpResponse(req, &check); !ok {
		for _, e := range errs {
			t.Errorf("contract: %s: %s", e.Message, e.Reason)
			for _, f := range e.SchemaValidationErrors {
				t.Errorf("  at %s: %s", f.FieldPath, f.Reason)
			}
		}
	}
}
