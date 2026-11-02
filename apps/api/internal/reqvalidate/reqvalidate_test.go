package reqvalidate

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/api"
)

var (
	sharedOnce sync.Once
	shared     *Validator
	sharedErr  error
)

func newValidator(t *testing.T) *Validator {
	t.Helper()
	sharedOnce.Do(func() { shared, sharedErr = New(api.Spec) })
	if sharedErr != nil {
		t.Fatal(sharedErr)
	}
	return shared
}

func check(t *testing.T, method, path, specPath, body string) Outcome {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", `"8e03978e-40d5-43e8-bc93-6894a57f9324"`)
	out, err := newValidator(t).Check(r, method, specPath, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func putProfile(t *testing.T, body string) Outcome {
	t.Helper()
	return check(t, "PUT", "/v1/seller-profile", "/seller-profile", body)
}

type fe struct{ Pointer, Code string }

func codes(fields []api.FieldError) []fe {
	out := []fe{}
	for _, f := range fields {
		out = append(out, fe{f.Pointer, f.Code})
	}
	return out
}

func TestValidBodyPasses(t *testing.T) {
	out := putProfile(t, `{"legal_name":"Beispiel GmbH","city":"Berlin","country_code":"DE","contact_email":"rechnung@example.com","jp_registration_number":null}`)
	if len(out.Fields) != 0 || len(out.Params) != 0 {
		t.Errorf("valid body rejected: %+v", out)
	}
}

func TestFieldErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []fe
	}{
		{"unknown field alone", `{"legal_nam":"x"}`, []fe{{"/legal_nam", CodeUnknownField}}},
		// The false positive from ADR 0010: the library reports city (valid
		// and declared) under unevaluatedProperties because legal_name failed.
		{"declared field next to an invalid one", `{"legal_name":5,"city":"Berlin"}`, []fe{{"/legal_name", CodeInvalidFormat}}},
		{"invalid and unknown together", `{"legal_name":5,"city":"Berlin","cty":"x"}`, []fe{{"/cty", CodeUnknownField}, {"/legal_name", CodeInvalidFormat}}},
		{"too long", `{"legal_name":"` + strings.Repeat("a", 201) + `"}`, []fe{{"/legal_name", CodeTooLong}}},
		{"pattern", `{"jp_registration_number":"T12"}`, []fe{{"/jp_registration_number", CodeInvalidFormat}}},
		{"email format", `{"contact_email":"nope"}`, []fe{{"/contact_email", CodeInvalidFormat}}},
		// country_code is oneOf [CountryCode, null]: one error, not the null
		// branch's "want null".
		{"nullable oneOf", `{"country_code":"de"}`, []fe{{"/country_code", CodeInvalidFormat}}},
		{"not an object", `[]`, []fe{{"", CodeInvalidFormat}}},
		{"key needing escapes", `{"a/b~c":1}`, []fe{{"/a~1b~0c", CodeUnknownField}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := codes(putProfile(t, c.body).Fields)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("errors = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDetailIsBounded(t *testing.T) {
	out := putProfile(t, `{"jp_registration_number":"`+strings.Repeat("x", 5000)+`"}`)
	if len(out.Fields) != 1 || len(out.Fields[0].Detail) > maxDetail+len("…") {
		t.Errorf("fields = %+v", out.Fields)
	}

	// A multi-byte value is cut between characters, never inside one.
	out = putProfile(t, `{"jp_registration_number":"`+strings.Repeat("株", 300)+`"}`)
	if len(out.Fields) != 1 || !utf8.ValidString(out.Fields[0].Detail) {
		t.Errorf("detail is not valid UTF-8: %q", out.Fields[0].Detail)
	}
}

// Required fields and nested arrays, on endpoints this slice does not serve
// yet, so the mapping is ready for them.
func TestRequiredAndNested(t *testing.T) {
	got := codes(check(t, "POST", "/v1/customers", "/customers", `{}`).Fields)
	if want := []fe{{"/kind", CodeRequired}, {"/name", CodeRequired}}; !reflect.DeepEqual(got, want) {
		t.Errorf("customers {}: %v, want %v", got, want)
	}

	body := `{"customer_id":"0b6c5e2e-1111-4222-8333-944455556666","lines":[{"name":"a","quantity":2,"unit_price":"1","tax_rate":"19","bogus":1}]}`
	got = codes(check(t, "POST", "/v1/invoices", "/invoices", body).Fields)
	want := []fe{{"/lines/0/bogus", CodeUnknownField}, {"/lines/0/quantity", CodeInvalidFormat}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("invoice: %v, want %v", got, want)
	}
}

func TestBadQueryParameterIsParamError(t *testing.T) {
	out := check(t, "GET", "/v1/invoices?limit=abc", "/invoices", "")
	if len(out.Params) != 1 || len(out.Fields) != 0 {
		t.Errorf("outcome = %+v", out)
	}
}

func TestConcurrentChecks(t *testing.T) {
	v := newValidator(t)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			body := `{"legal_name":5,"city":"Berlin"}`
			r := httptest.NewRequest("PUT", "/v1/seller-profile", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			out, err := v.Check(r, "PUT", "/seller-profile", []byte(body))
			if err != nil || len(out.Fields) != 1 {
				t.Errorf("out = %+v, err = %v", out, err)
			}
		})
	}
	wg.Wait()
}

func TestEscape(t *testing.T) {
	for in, want := range map[string]string{"a": "a", "a/b": "a~1b", "a~b": "a~0b", "~/": "~0~1"} {
		if got := escape(in); got != want {
			t.Errorf("escape(%q) = %q, want %q", in, got, want)
		}
	}
}
