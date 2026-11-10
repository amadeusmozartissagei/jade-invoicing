// Package reqvalidate checks requests against the contract at run time and
// maps the failures onto the API's field errors: a JSON Pointer into the body
// and a code from docs/api/errors.md (ADR 0010, ADR 0012).
//
// The contract checks structure (types, required fields, lengths, patterns,
// enums, unknown fields). Domain rules the spec cannot express, such as
// currency decimals, are checked by the handlers.
package reqvalidate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
	"github.com/pb33f/libopenapi-validator/config"
	verrors "github.com/pb33f/libopenapi-validator/errors"
	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/api"
)

// Field error codes from docs/api/errors.md that the contract can produce.
const (
	CodeRequired      = "required"
	CodeUnknownField  = "unknown_field"
	CodeInvalidFormat = "invalid_format"
	CodeInvalidValue  = "invalid_value"
	CodeTooLong       = "too_long"
	CodeTooManyItems  = "too_many_items"
	CodeOutOfRange    = "out_of_range"
	CodeNotAllowed    = "not_allowed"
)

// maxDetail caps a field error's detail; the library's reason can quote the
// client's value, which may be long.
const maxDetail = 200

// Validator checks requests against one OpenAPI document. It is safe for
// concurrent use.
type Validator struct {
	v     validator.Validator
	model *v3.Document
}

// New parses spec. It takes about 170 ms for openapi.yaml, so the server
// builds it once, on the first request that needs it.
func New(spec []byte) (*Validator, error) {
	doc, err := libopenapi.NewDocument(spec)
	if err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	model, err := doc.BuildV3Model()
	if err != nil {
		return nil, fmt.Errorf("build spec model: %w", err)
	}
	// Authentication is checked by the server itself, before validation.
	// Format assertions make "format: email" and the like count, as the
	// contract intends; JSON Schema 2020-12 treats format as a note otherwise.
	v, errs := validator.NewValidator(doc, config.WithoutSecurityValidation(), config.WithFormatAssertions())
	if len(errs) > 0 {
		return nil, fmt.Errorf("build validator: %w", errors.Join(errs...))
	}
	return &Validator{v: v, model: &model.Model}, nil
}

// Outcome is what is wrong with a request. Params go to a 400 bad_request
// (errors.md: "a query parameter of the wrong type"); Fields go to a 422
// validation_failed. Both empty means the request is valid.
type Outcome struct {
	Params []string
	Fields []api.FieldError
}

// Check validates r against the operation for method and specPath (the path
// template as written in openapi.yaml, without the /v1 server prefix). body
// is r's body, already read; r.Body is not used.
func (v *Validator) Check(r *http.Request, method, specPath string, body []byte) (Outcome, error) {
	req := r.Clone(r.Context())
	req.Body = http.NoBody
	if body != nil {
		req.Body = io.NopCloser(bytes.NewReader(body))
	}

	ok, errs := v.v.ValidateHttpRequestSync(req)
	if ok {
		return Outcome{}, nil
	}

	var out Outcome
	var failures []*verrors.SchemaValidationFailure
	bodyFailed := false
	for _, e := range errs {
		switch e.ValidationType {
		case "parameter":
			out.Params = append(out.Params, e.Message)
		case "requestBody":
			bodyFailed = true
			failures = append(failures, e.SchemaValidationErrors...)
		default:
			// A route the server registers but the contract lacks, or a
			// content type the server let through: a bug, not the client's.
			return Outcome{}, fmt.Errorf("unexpected validation failure (%s/%s): %s", e.ValidationType, e.ValidationSubType, e.Message)
		}
	}
	if !bodyFailed {
		return out, nil
	}

	out.Fields = fieldErrors(failures)
	var doc any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if dec.Decode(&doc) == nil {
		nullNotAllowed(doc, out.Fields)
		if root := v.bodySchema(method, specPath, req.Header.Get("Content-Type")); root != nil {
			unknownFields(root, doc, "", &out.Fields)
		}
	}
	if len(out.Fields) == 0 {
		// The library rejected the body for a reason none of the rules above
		// recognised. Still a 422, never a silent pass.
		out.Fields = []api.FieldError{{Pointer: "", Code: CodeInvalidValue, Detail: "The request body does not match the schema."}}
	}
	sort.SliceStable(out.Fields, func(i, j int) bool { return out.Fields[i].Pointer < out.Fields[j].Pointer })
	return out, nil
}

// fieldErrors maps the library's failures to one field error per pointer.
// additionalProperties and unevaluatedProperties failures are dropped here:
// the library also reports valid, declared fields under
// unevaluatedProperties whenever another field fails (correct JSON Schema
// 2020-12 behaviour, ADR 0010), so unknown fields are found by unknownFields
// instead, from the schema's declared properties.
func fieldErrors(failures []*verrors.SchemaValidationFailure) []api.FieldError {
	var order []string
	byPointer := map[string][]candidate{}
	add := func(c candidate) {
		if _, seen := byPointer[c.Pointer]; !seen {
			order = append(order, c.Pointer)
		}
		byPointer[c.Pointer] = append(byPointer[c.Pointer], c)
	}

	for _, f := range failures {
		keyword := lastSegment(f.KeywordLocation)
		parent := pointer(f.InstancePath)
		switch keyword {
		case "additionalProperties", "unevaluatedProperties":
			continue
		case "required":
			for _, name := range quotedNames(f.Reason) {
				add(candidate{FieldError: api.FieldError{Pointer: parent + "/" + escape(name), Code: CodeRequired, Detail: name + " is required"}})
			}
			continue
		}
		add(candidate{
			FieldError: api.FieldError{Pointer: parent, Code: codeFor(keyword), Detail: detail(f)},
			nullBranch: keyword == "type" && strings.HasSuffix(f.Reason, "want null"),
		})
	}

	out := make([]api.FieldError, 0, len(order))
	for _, p := range order {
		out = append(out, pick(byPointer[p]))
	}
	return out
}

type candidate struct {
	api.FieldError
	// nullBranch marks the null branch of a nullable field written as
	// oneOf [schema, null]: it fails for every non-null value, so it says
	// nothing about what is wrong.
	nullBranch bool
}

// pick chooses one error for a pointer, preferring any error over a
// null-branch one.
func pick(cs []candidate) api.FieldError {
	for _, c := range cs {
		if !c.nullBranch {
			return c.FieldError
		}
	}
	return cs[0].FieldError
}

func codeFor(keyword string) string {
	switch keyword {
	case "enum", "const":
		return CodeInvalidValue
	case "maxLength":
		return CodeTooLong
	case "maxItems":
		return CodeTooManyItems
	case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "minLength", "minItems":
		return CodeOutOfRange
	default: // type, format, pattern, and anything else about the value's shape
		return CodeInvalidFormat
	}
}

func detail(f *verrors.SchemaValidationFailure) string {
	d := f.Reason
	if f.FieldName != "" {
		d = f.FieldName + ": " + d
	}
	if len(d) > maxDetail {
		// Cut on a character boundary: names and addresses are often
		// Japanese, and half a character would reach the client as U+FFFD.
		d = strings.ToValidUTF8(d[:maxDetail], "") + "…"
	}
	return d
}

// nullNotAllowed turns a type failure on a JSON null into not_allowed
// (errors.md: "setting a non-nullable field to null"). Nullable fields accept
// null, so a null that fails is always a field that cannot be cleared, such
// as a customer's name in a merge patch.
func nullNotAllowed(doc any, fields []api.FieldError) {
	for i, f := range fields {
		if f.Code != CodeInvalidFormat || f.Pointer == "" {
			continue
		}
		if v, ok := lookup(doc, f.Pointer); ok && v == nil {
			fields[i].Code, fields[i].Detail = CodeNotAllowed, nullDetail(f.Pointer)
		}
	}
}

// nullDetail names the field: "name cannot be null", or for an array
// element "lines/0 cannot be null", since "0" alone names nothing.
func nullDetail(ptr string) string {
	cut := strings.LastIndex(ptr, "/")
	last := ptr[cut+1:]
	if _, err := strconv.Atoi(last); err == nil && cut > 0 {
		prev := strings.LastIndex(ptr[:cut], "/")
		return unescape(ptr[prev+1:cut]) + "/" + last + " cannot be null"
	}
	return unescape(last) + " cannot be null"
}

// lookup resolves a JSON Pointer (RFC 6901) in a decoded document.
func lookup(doc any, ptr string) (any, bool) {
	cur := doc
	for _, tok := range strings.Split(ptr, "/")[1:] {
		tok = unescape(tok)
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[tok]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(c) {
				return nil, false
			}
			cur = c[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

func unescape(tok string) string {
	return strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
}

// unknownFields reports every property in v that the schema s does not
// declare, where s is closed (additionalProperties or unevaluatedProperties
// false). Declared properties are collected across allOf, oneOf, and anyOf,
// which is how unevaluatedProperties sees them when the body is valid.
func unknownFields(s *base.Schema, v any, ptr string, out *[]api.FieldError) {
	switch val := v.(type) {
	case map[string]any:
		declared, closed := objectShape(s)
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := ptr + "/" + escape(k)
			schemas, ok := declared[k]
			if !ok {
				if closed {
					*out = append(*out, api.FieldError{Pointer: p, Code: CodeUnknownField, Detail: k + " is not a field of this request"})
				}
				continue
			}
			for _, cs := range schemas {
				unknownFields(cs, val[k], p, out)
			}
		}
	case []any:
		items := itemSchemas(s)
		for i, el := range val {
			for _, is := range items {
				unknownFields(is, el, ptr+"/"+strconv.Itoa(i), out)
			}
		}
	}
}

func objectShape(s *base.Schema) (declared map[string][]*base.Schema, closed bool) {
	declared = map[string][]*base.Schema{}
	var walk func(*base.Schema)
	walk = func(s *base.Schema) {
		if s == nil {
			return
		}
		if isFalse(s.AdditionalProperties) || isFalse(s.UnevaluatedProperties) {
			closed = true
		}
		if s.Properties != nil {
			for name, proxy := range s.Properties.FromOldest() {
				if ps := proxy.Schema(); ps != nil {
					declared[name] = append(declared[name], ps)
				}
			}
		}
		for _, group := range [][]*base.SchemaProxy{s.AllOf, s.OneOf, s.AnyOf} {
			for _, proxy := range group {
				walk(proxy.Schema())
			}
		}
	}
	walk(s)
	return declared, closed
}

func itemSchemas(s *base.Schema) []*base.Schema {
	var out []*base.Schema
	var walk func(*base.Schema)
	walk = func(s *base.Schema) {
		if s == nil {
			return
		}
		if s.Items != nil && s.Items.IsA() && s.Items.A != nil {
			if is := s.Items.A.Schema(); is != nil {
				out = append(out, is)
			}
		}
		for _, group := range [][]*base.SchemaProxy{s.AllOf, s.OneOf, s.AnyOf} {
			for _, proxy := range group {
				walk(proxy.Schema())
			}
		}
	}
	walk(s)
	return out
}

func isFalse(d *base.DynamicValue[*base.SchemaProxy, bool]) bool {
	return d != nil && d.IsB() && !d.B
}

func (v *Validator) bodySchema(method, specPath, contentType string) *base.Schema {
	if v.model.Paths == nil || v.model.Paths.PathItems == nil {
		return nil
	}
	item, ok := v.model.Paths.PathItems.Get(specPath)
	if !ok || item == nil {
		return nil
	}
	var op *v3.Operation
	switch method {
	case http.MethodPost:
		op = item.Post
	case http.MethodPut:
		op = item.Put
	case http.MethodPatch:
		op = item.Patch
	}
	if op == nil || op.RequestBody == nil || op.RequestBody.Content == nil {
		return nil
	}
	mediaType, _, _ := strings.Cut(contentType, ";")
	mt, ok := op.RequestBody.Content.Get(strings.ToLower(strings.TrimSpace(mediaType)))
	if !ok || mt == nil || mt.Schema == nil {
		return nil
	}
	return mt.Schema.Schema()
}

// pointer joins the library's instance path. Its segments are already
// escaped for JSON Pointer (a key "a/b" arrives as "a~1b").
func pointer(segments []string) string {
	if len(segments) == 0 {
		return ""
	}
	return "/" + strings.Join(segments, "/")
}

// escape encodes one reference token (RFC 6901 §3).
func escape(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}

func lastSegment(keywordLocation string) string {
	return keywordLocation[strings.LastIndex(keywordLocation, "/")+1:]
}

// quotedNames reads the property names from a "required" failure:
// "missing property 'name'" or "missing properties 'kind', 'name'". The names
// come from the contract, which uses snake_case only, so they never contain
// a quote.
var quoted = regexp.MustCompile(`'([^']*)'`)

func quotedNames(reason string) []string {
	var names []string
	for _, m := range quoted.FindAllStringSubmatch(reason, -1) {
		names = append(names, m[1])
	}
	return names
}
