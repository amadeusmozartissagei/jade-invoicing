package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/admin"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/contracttest"
)

// Tests for invoice drafts (ADR 0015). "Criterion n" refers to the
// acceptance criteria listed there.

type invoiceResp = map[string]any

const deLines = `[{"name":"Consulting, October","quantity":"10","unit_code":"HUR","unit_price":"95.00","tax_rate":"19"},` +
	`{"name":"Technical book","quantity":"1","unit_price":"39.90","tax_rate":"7"}]`

func (s dbServer) createInvoice(t *testing.T, key, idemKey, body string) (*http.Request, *http.Response) {
	t.Helper()
	return s.send(t, key, "POST", "/v1/invoices", body, map[string]string{"Idempotency-Key": idemKey})
}

func (s dbServer) newInvoice(t *testing.T, c admin.Created, body string) invoiceResp {
	t.Helper()
	req, resp := s.createInvoice(t, c.Key, uuid.NewString(), body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create invoice: status %d; %s; log: %s", resp.StatusCode, readAll(t, resp), s.logs)
	}
	contracttest.ValidateResponse(t, req, resp)
	var got invoiceResp
	decodeJSON(t, resp, &got)
	return got
}

func (s dbServer) customerID(t *testing.T, c admin.Created) string {
	t.Helper()
	return s.newCustomer(t, c, customerJSON)["id"].(string)
}

func (s dbServer) patchInvoice(t *testing.T, key, id, etag, body string) (*http.Request, *http.Response) {
	t.Helper()
	return s.send(t, key, "PATCH", "/v1/invoices/"+id, body, map[string]string{"If-Match": etag})
}

// issue marks a draft issued directly in the database, standing in for the
// issue endpoint, which comes with the next slice.
func (s dbServer) issue(t *testing.T, id string) {
	t.Helper()
	s.exec(t, `
		update app.invoices
		set status = 'issued', number = 'TEST-' || id,
		    sequence_value = (select coalesce(max(i.sequence_value), 0) + 1 from app.invoices i where i.series_id = invoices.series_id), issue_date = current_date,
		    issued_at = now(), retention_until = '2035-12-31',
		    tax_rounding = case jurisdiction when 'DE' then 'half_away_from_zero' else 'down' end,
		    supply_date = coalesce(supply_date, current_date)
		where id = $1`, id)
}

func wantFields(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	for k, w := range want {
		if v, ok := got[k]; !ok || !reflect.DeepEqual(v, w) {
			t.Errorf("%s = %#v (present %v), want %#v", k, v, ok, w)
		}
	}
}

// wantFieldErrors checks a 422 validation_failed and its pointers and codes.
func wantFieldErrors(t *testing.T, req *http.Request, resp *http.Response, want map[string]string) {
	t.Helper()
	body := readAll(t, resp)
	wantProblem(t, req, resp, http.StatusUnprocessableEntity, "validation_failed")
	var p struct {
		Errors []struct{ Pointer, Code string } `json:"errors"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range p.Errors {
		got[e.Pointer] = e.Code
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors = %v, want %v", got, want)
	}
}

// Criterion 1: a German draft with two rates, tax once per rate.
func TestCreateGermanDraft(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	cust := s.customerID(t, c)

	req, resp := s.createInvoice(t, c.Key, "k1", fmt.Sprintf(
		`{"customer_id":%q,"supply_date":"2026-10-30","due_date":"2026-11-30","lines":%s}`, cust, deLines))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d; %s; log: %s", resp.StatusCode, readAll(t, resp), s.logs)
	}
	contracttest.ValidateResponse(t, req, resp)
	if et := resp.Header.Get("ETag"); et != `"1"` {
		t.Errorf("ETag = %q", et)
	}
	var got invoiceResp
	decodeJSON(t, resp, &got)
	if loc := resp.Header.Get("Location"); loc != "/v1/invoices/"+got["id"].(string) {
		t.Errorf("Location = %q", loc)
	}
	wantFields(t, got, map[string]any{
		"document_type": "invoice", "status": "draft", "number": nil, "customer_id": cust,
		"preceding_invoice_id": nil, "jurisdiction": "DE", "currency": "EUR", "issue_date": nil,
		"supply_date": "2026-10-30", "due_date": "2026-11-30", "buyer_reference": "-", "tax_rounding": nil,
		"line_net_total": "989.90", "allowance_total": "0.00", "charge_total": "0.00",
		"tax_exclusive_total": "989.90", "tax_total": "183.29", "tax_inclusive_total": "1173.19",
		"prepaid_amount": "0.00", "rounding_amount": "0.00", "amount_due": "1173.19",
		"amount_paid": "0.00", "payment_status": nil, "version": 1.0, "parties": []any{},
		"retention_until": nil, "issued_at": nil,
	})
	wantFields(t, got["lines"].([]any)[0].(map[string]any), map[string]any{
		"position": 1.0, "name": "Consulting, October", "description": nil, "quantity": "10", "unit_code": "HUR",
		"unit_price": "95", "price_base_quantity": "1", "tax_category": "S", "tax_rate": "19",
		"line_net_amount": "950.00", "period_start": nil, "period_end": nil,
	})
	wantFields(t, got["lines"].([]any)[1].(map[string]any), map[string]any{
		"position": 2.0, "unit_code": "C62", "line_net_amount": "39.90", "tax_rate": "7",
	})
	want := []any{
		map[string]any{"tax_category": "S", "tax_rate": "19", "taxable_amount": "950.00", "tax_amount": "180.50"},
		map[string]any{"tax_category": "S", "tax_rate": "7", "taxable_amount": "39.90", "tax_amount": "2.79"},
	}
	if !reflect.DeepEqual(got["tax_breakdown"], want) {
		t.Errorf("tax_breakdown = %v", got["tax_breakdown"])
	}
	if n := s.count(t, `select count(*) from app.audit_log where action = 'invoice.create' and subject_id = $1`, got["id"]); n != 1 {
		t.Errorf("%d audit entries", n)
	}

	// The same request again is replayed with the same ETag (ADR 0014).
	_, again := s.createInvoice(t, c.Key, "k1", fmt.Sprintf(
		`{"customer_id":%q,"supply_date":"2026-10-30","due_date":"2026-11-30","lines":%s}`, cust, deLines))
	if again.StatusCode != http.StatusCreated || again.Header.Get("Idempotent-Replayed") != "true" || again.Header.Get("ETag") != `"1"` {
		t.Errorf("replay: status %d, headers %v", again.StatusCode, again.Header)
	}
}

// Criterion 1: the buyer reference defaults to the customer's.
func TestBuyerReferenceDefault(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	cust := s.newCustomer(t, c, `{"kind":"business","name":"Leitweg GmbH","default_buyer_reference":"04011000-12345-67"}`)["id"].(string)

	got := s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q}`, cust))
	wantFields(t, got, map[string]any{"buyer_reference": "04011000-12345-67", "lines": []any{}, "tax_breakdown": []any{}, "amount_due": "0.00"})

	got = s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q,"buyer_reference":null}`, cust))
	wantFields(t, got, map[string]any{"buyer_reference": nil})

	// The "-" placeholder is a German rule; a Japanese draft gets none.
	jp := s.tenant(t, "JP")
	got = s.newInvoice(t, jp, fmt.Sprintf(`{"customer_id":%q}`, s.customerID(t, jp)))
	wantFields(t, got, map[string]any{"buyer_reference": nil})
}

// Criterion 2: a Japanese draft rounds with the tenant's mode, once per rate.
func TestJapaneseDraftUsesTenantRounding(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "JP") // rounding "down"
	cust := s.customerID(t, c)

	got := s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q,"supply_date":"2026-10-30","lines":[`+
		`{"name":"Rice","quantity":"3","unit_price":"333","tax_rate":"10"},`+
		`{"name":"Tea","quantity":"1","unit_price":"1234","tax_rate":"8"}]}`, cust))
	wantFields(t, got, map[string]any{
		"currency": "JPY", "line_net_total": "2233", "tax_total": "197", "tax_inclusive_total": "2430", "amount_paid": "0",
	})

	// The tenant switches to rounding up; the next edit recomputes the tax.
	s.exec(t, `update app.tenants set jp_tax_rounding = 'up' where id = $1`, c.TenantID)
	req, resp := s.patchInvoice(t, c.Key, got["id"].(string), `"1"`, `{"note":"rounding changed"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: %d %s", resp.StatusCode, readAll(t, resp))
	}
	contracttest.ValidateResponse(t, req, resp)
	decodeJSON(t, resp, &got)
	wantFields(t, got, map[string]any{"tax_total": "199", "tax_inclusive_total": "2432"}) // 99.9 → 100, 98.72 → 99
	want := []any{
		map[string]any{"tax_category": "S", "tax_rate": "10", "taxable_amount": "999", "tax_amount": "100"},
		map[string]any{"tax_category": "S", "tax_rate": "8", "taxable_amount": "1234", "tax_amount": "99"},
	}
	if !reflect.DeepEqual(got["tax_breakdown"], want) {
		t.Errorf("tax_breakdown = %v", got["tax_breakdown"])
	}
}

// Criterion 3: domain rules the contract cannot express, each with a pointer.
// Nothing is written on a 422.
func TestDraftDomainValidation(t *testing.T) {
	s := newDBServer(t)
	de, jp, other := s.tenant(t, "DE"), s.tenant(t, "JP"), s.tenant(t, "DE")
	cust, jpCust, otherCust := s.customerID(t, de), s.customerID(t, jp), s.customerID(t, other)

	for name, tc := range map[string]struct {
		c    admin.Created
		body string
		want map[string]string
	}{
		"line rules": {de, fmt.Sprintf(`{"customer_id":%q,"lines":[`+
			`{"name":"a","quantity":"0","unit_price":"1","tax_rate":"19"},`+
			`{"name":"b","quantity":"1","unit_price":"1","price_base_quantity":"0","tax_rate":"19"},`+
			`{"name":"c","quantity":"1","unit_price":"1","tax_rate":"16"},`+
			`{"name":"d","quantity":"1","unit_price":"1","tax_rate":"19","period_start":"2026-10-31","period_end":"2026-10-01"}]}`, cust),
			map[string]string{
				"/lines/0/quantity": "must_be_positive", "/lines/1/price_base_quantity": "must_be_positive",
				"/lines/2/tax_rate": "rate_not_valid", "/lines/3/period_end": "invalid_period",
			}},
		"rate before its validity": {de, fmt.Sprintf(`{"customer_id":%q,"supply_date":"2024-12-31","lines":`+
			`[{"name":"a","quantity":"1","unit_price":"1","tax_rate":"19"}]}`, cust),
			map[string]string{"/lines/0/tax_rate": "rate_not_valid"}},
		"rate before its validity, supply period start only": {de, fmt.Sprintf(`{"customer_id":%q,"supply_period_start":"2024-12-01",`+
			`"lines":[{"name":"a","quantity":"1","unit_price":"1","tax_rate":"19"}]}`, cust),
			map[string]string{"/lines/0/tax_rate": "rate_not_valid"}},
		"supply period backwards": {de, fmt.Sprintf(`{"customer_id":%q,"supply_period_start":"2026-11-01",`+
			`"supply_period_end":"2026-10-01","lines":[{"name":"a","quantity":"1","unit_price":"1","tax_rate":"7"}]}`, cust),
			map[string]string{"/supply_period_end": "invalid_period"}},
		"a German rate in Japan": {jp, fmt.Sprintf(`{"customer_id":%q,"lines":[{"name":"a","quantity":"1","unit_price":"1","tax_rate":"19"}]}`, jpCust),
			map[string]string{"/lines/0/tax_rate": "rate_not_valid"}},
		"yen with decimals": {jp, fmt.Sprintf(`{"customer_id":%q,"prepaid_amount":"100.50"}`, jpCust),
			map[string]string{"/prepaid_amount": "too_many_decimals"}},
		"too large": {de, fmt.Sprintf(`{"customer_id":%q,"lines":[{"name":"a","quantity":"999999999999","unit_price":"999999999999","tax_rate":"19"}]}`, cust),
			map[string]string{"/lines/0": "out_of_range"}},
		"no customer":           {de, `{}`, map[string]string{"/customer_id": "required"}},
		"unknown customer":      {de, fmt.Sprintf(`{"customer_id":%q}`, uuid.NewString()), map[string]string{"/customer_id": "invalid_value"}},
		"other tenant customer": {de, fmt.Sprintf(`{"customer_id":%q}`, otherCust), map[string]string{"/customer_id": "invalid_value"}},
		"unknown series": {de, fmt.Sprintf(`{"customer_id":%q,"series_id":%q}`, cust, uuid.NewString()),
			map[string]string{"/series_id": "invalid_value"}},
		"preceding on an invoice": {de, fmt.Sprintf(`{"customer_id":%q,"preceding_invoice_id":%q}`, cust, uuid.NewString()),
			map[string]string{"/preceding_invoice_id": "not_allowed"}},
		"credit note without preceding": {de, fmt.Sprintf(`{"document_type":"credit_note","customer_id":%q}`, cust),
			map[string]string{"/preceding_invoice_id": "required"}},
		"unknown preceding": {de, fmt.Sprintf(`{"document_type":"corrected_invoice","preceding_invoice_id":%q}`, uuid.NewString()),
			map[string]string{"/preceding_invoice_id": "invalid_value"}},
		"amount as a JSON number": {de, fmt.Sprintf(`{"customer_id":%q,"prepaid_amount":5}`, cust),
			map[string]string{"/prepaid_amount": "invalid_format"}},
	} {
		t.Run(name, func(t *testing.T) {
			req, resp := s.createInvoice(t, tc.c.Key, uuid.NewString(), tc.body)
			wantFieldErrors(t, req, resp, tc.want)
		})
	}
	if n := s.count(t, `select count(*) from app.invoices where tenant_id in ($1, $2)`, de.TenantID, jp.TenantID); n != 0 {
		t.Errorf("%d invoices written", n)
	}
}

// Criterion 4: credit notes and corrected invoices refer to an issued
// invoice and are for its customer.
func TestCreditNoteAndCorrectionDrafts(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	cust, otherCust := s.customerID(t, c), s.customerID(t, c)
	original := s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q,"supply_date":"2026-10-30","lines":%s}`, cust, deLines))
	origID := original["id"].(string)

	// A draft cannot be credited.
	req, resp := s.createInvoice(t, c.Key, uuid.NewString(), fmt.Sprintf(`{"document_type":"credit_note","preceding_invoice_id":%q}`, origID))
	wantProblem(t, req, resp, http.StatusConflict, "invoice_not_issued")

	s.issue(t, origID)
	credit := s.newInvoice(t, c, fmt.Sprintf(`{"document_type":"credit_note","preceding_invoice_id":%q,"supply_date":"2026-10-30",`+
		`"lines":[{"name":"Discount","quantity":"1","unit_price":"50.00","tax_rate":"19"}]}`, origID))
	wantFields(t, credit, map[string]any{
		"document_type": "credit_note", "preceding_invoice_id": origID, "customer_id": cust,
		"tax_inclusive_total": "59.50", "payment_status": nil,
	})
	correction := s.newInvoice(t, c, fmt.Sprintf(`{"document_type":"corrected_invoice","preceding_invoice_id":%q,"customer_id":%q}`, origID, cust))
	wantFields(t, correction, map[string]any{"document_type": "corrected_invoice", "customer_id": cust})

	req, resp = s.createInvoice(t, c.Key, uuid.NewString(), fmt.Sprintf(
		`{"document_type":"credit_note","preceding_invoice_id":%q,"customer_id":%q}`, origID, otherCust))
	wantFieldErrors(t, req, resp, map[string]string{"/customer_id": "must_match_preceding"})

	req, resp = s.patchInvoice(t, c.Key, credit["id"].(string), `"1"`, fmt.Sprintf(`{"customer_id":%q}`, otherCust))
	wantFieldErrors(t, req, resp, map[string]string{"/customer_id": "must_match_preceding"})

	// A credit note cannot be credited.
	s.issue(t, credit["id"].(string))
	req, resp = s.createInvoice(t, c.Key, uuid.NewString(), fmt.Sprintf(
		`{"document_type":"credit_note","preceding_invoice_id":%q}`, credit["id"]))
	wantProblem(t, req, resp, http.StatusConflict, "invoice_not_issued")
}

// Criterion 5.
func TestGetInvoice(t *testing.T) {
	s := newDBServer(t)
	a, b := s.tenant(t, "DE"), s.tenant(t, "DE")
	inv := s.newInvoice(t, a, fmt.Sprintf(`{"customer_id":%q,"lines":%s}`, s.customerID(t, a), deLines))
	id := inv["id"].(string)

	req, resp := s.send(t, a.Key, "GET", "/v1/invoices/"+id, "", nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("ETag") != `"1"` {
		t.Fatalf("status %d, ETag %q", resp.StatusCode, resp.Header.Get("ETag"))
	}
	contracttest.ValidateResponse(t, req, resp)
	var got invoiceResp
	decodeJSON(t, resp, &got)
	if !reflect.DeepEqual(got, inv) {
		t.Errorf("GET = %v\nPOST = %v", got, inv)
	}

	req, resp = s.send(t, b.Key, "GET", "/v1/invoices/"+id, "", nil)
	wantProblem(t, req, resp, http.StatusNotFound, "not_found")
	req, resp = s.send(t, a.Key, "GET", "/v1/invoices/"+uuid.NewString(), "", nil)
	wantProblem(t, req, resp, http.StatusNotFound, "not_found")
}

// Criterion 6: merge patch with If-Match.
func TestPatchDraft(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	cust := s.customerID(t, c)
	inv := s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q,"note":"first","order_reference":"PO-1","lines":%s}`, cust, deLines))
	id := inv["id"].(string)

	req, resp := s.send(t, c.Key, "PATCH", "/v1/invoices/"+id, `{"note":"x"}`, nil)
	wantProblem(t, req, resp, http.StatusPreconditionRequired, "precondition_required")
	for _, stale := range []string{`"2"`, `W/"1"`, `1`} {
		req, resp = s.patchInvoice(t, c.Key, id, stale, `{"note":"x"}`)
		wantProblem(t, req, resp, http.StatusPreconditionFailed, "precondition_failed")
	}

	// Replace the lines, clear the note, keep the order reference.
	req, resp = s.patchInvoice(t, c.Key, id, `"0", "1"`, `{"note":null,"lines":[{"name":"Only line","quantity":"2","unit_price":"10.005","tax_rate":"7"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: %d %s; log: %s", resp.StatusCode, readAll(t, resp), s.logs)
	}
	contracttest.ValidateResponse(t, req, resp)
	if et := resp.Header.Get("ETag"); et != `"2"` {
		t.Errorf("ETag = %q", et)
	}
	var got invoiceResp
	decodeJSON(t, resp, &got)
	wantFields(t, got, map[string]any{
		"note": nil, "order_reference": "PO-1", "version": 2.0, "line_net_total": "20.01", "tax_total": "1.40", "amount_due": "21.41",
	})
	if lines := got["lines"].([]any); len(lines) != 1 {
		t.Errorf("lines = %v", lines)
	}
	if n := s.count(t, `select count(*) from app.invoice_tax_breakdowns where invoice_id = $1`, id); n != 1 {
		t.Errorf("%d breakdown rows", n)
	}
	var details string
	if err := s.admin.QueryRow(t.Context(), `select details::text from app.audit_log where action = 'invoice.update' and subject_id = $1`, id).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if details != `{"fields": ["note", "lines"]}` {
		t.Errorf("audit details = %s", details)
	}

	// A patch that changes nothing keeps the version; "*" matches any.
	for _, body := range []string{`{}`, `{"order_reference":"PO-1","lines":[{"name":"Only line","quantity":"2.0","unit_price":"10.005","tax_rate":"7.00"}]}`} {
		_, resp = s.patchInvoice(t, c.Key, id, `*`, body)
		decodeJSON(t, resp, &got)
		if resp.StatusCode != http.StatusOK || got["version"] != 2.0 {
			t.Errorf("%s: status %d, version %v", body, resp.StatusCode, got["version"])
		}
	}

	// An advance payment lowers the amount due.
	req, resp = s.patchInvoice(t, c.Key, id, `"2"`, `{"prepaid_amount":"1.41"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch prepaid: %d %s", resp.StatusCode, readAll(t, resp))
	}
	contracttest.ValidateResponse(t, req, resp)
	decodeJSON(t, resp, &got)
	wantFields(t, got, map[string]any{"prepaid_amount": "1.41", "amount_due": "20.00", "version": 3.0})

	// A date that makes the stored lines' rate invalid is reported at the
	// date, since the lines are not in this request body.
	req, resp = s.patchInvoice(t, c.Key, id, `"3"`, `{"supply_date":"2024-12-31"}`)
	wantFieldErrors(t, req, resp, map[string]string{"/supply_date": "rate_not_valid"})

	// Fields that cannot be null.
	req, resp = s.patchInvoice(t, c.Key, id, `"3"`, `{"lines":null,"customer_id":null}`)
	wantFieldErrors(t, req, resp, map[string]string{"/lines": "not_allowed", "/customer_id": "not_allowed"})

	// Issued documents cannot change.
	s.issue(t, id)
	req, resp = s.patchInvoice(t, c.Key, id, `"3"`, `{"note":"late"}`)
	wantProblem(t, req, resp, http.StatusConflict, "invoice_not_draft")
}

// Criterion 6: two edits from the same ETag; exactly one applies.
func TestConcurrentPatchesFromOneETag(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	id := s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q}`, s.customerID(t, c)))["id"].(string)

	const n = 8
	var wg sync.WaitGroup
	statuses := make([]int, n)
	for i := range n {
		wg.Go(func() {
			_, resp := s.patchInvoice(t, c.Key, id, `"1"`, fmt.Sprintf(`{"note":"edit %d"}`, i))
			statuses[i] = resp.StatusCode
		})
	}
	wg.Wait()
	ok, failed := 0, 0
	for _, st := range statuses {
		switch st {
		case http.StatusOK:
			ok++
		case http.StatusPreconditionFailed:
			failed++
		}
	}
	if ok != 1 || failed != n-1 {
		t.Errorf("statuses = %v, want one 200 and %d 412", statuses, n-1)
	}
	if v := s.count(t, `select version from app.invoices where id = $1`, id); v != 2 {
		t.Errorf("version = %d", v)
	}
}

// Criterion 7.
func TestDeleteDraft(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	cust := s.customerID(t, c)
	id := s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q,"lines":%s}`, cust, deLines))["id"].(string)

	req, resp := s.send(t, c.Key, "DELETE", "/v1/invoices/"+id, "", nil)
	wantProblem(t, req, resp, http.StatusPreconditionRequired, "precondition_required")
	req, resp = s.send(t, c.Key, "DELETE", "/v1/invoices/"+id, "", map[string]string{"If-Match": `"7"`})
	wantProblem(t, req, resp, http.StatusPreconditionFailed, "precondition_failed")

	req, resp = s.send(t, c.Key, "DELETE", "/v1/invoices/"+id, "", map[string]string{"If-Match": `"1"`})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d %s", resp.StatusCode, readAll(t, resp))
	}
	contracttest.ValidateResponse(t, req, resp)
	if n := s.count(t, `select count(*) from app.invoice_lines where invoice_id = $1`, id) +
		s.count(t, `select count(*) from app.invoices where id = $1`, id); n != 0 {
		t.Errorf("%d rows left", n)
	}
	req, resp = s.send(t, c.Key, "DELETE", "/v1/invoices/"+id, "", map[string]string{"If-Match": `"1"`})
	wantProblem(t, req, resp, http.StatusNotFound, "not_found")

	issued := s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q}`, cust))["id"].(string)
	s.issue(t, issued)
	req, resp = s.send(t, c.Key, "DELETE", "/v1/invoices/"+issued, "", map[string]string{"If-Match": `"1"`})
	wantProblem(t, req, resp, http.StatusConflict, "invoice_not_draft")
}

// Criterion 8: 250 invoices in pages of 100, each once, newest first, with
// ties in created_at broken by id.
func TestListInvoicesPaging(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	cust := s.customerID(t, c)
	other := s.customerID(t, c)
	s.exec(t, `
		insert into app.invoices (tenant_id, document_type, customer_id, series_id, jurisdiction, currency, created_at)
		select $1, 'invoice', $2, (select id from app.number_series where tenant_id = $1), 'DE', 'EUR',
		       '2026-11-01'::timestamptz + (g % 7) * interval '1 second'
		from generate_series(1, 250) g`, c.TenantID, cust)
	marked := s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q}`, other))["id"].(string)

	var seen []string
	cursor := ""
	for page := 0; ; page++ {
		path := "/v1/invoices?limit=100&customer_id=" + cust
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		req, resp := s.send(t, c.Key, "GET", path, "", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("page %d: %d %s", page, resp.StatusCode, readAll(t, resp))
		}
		contracttest.ValidateResponse(t, req, resp)
		var body struct {
			Data []struct {
				ID        string `json:"id"`
				CreatedAt string `json:"created_at"`
			} `json:"data"`
			NextCursor *string `json:"next_cursor"`
		}
		decodeJSON(t, resp, &body)
		for _, inv := range body.Data {
			seen = append(seen, inv.CreatedAt+" "+inv.ID)
		}
		if body.NextCursor == nil {
			break
		}
		cursor = *body.NextCursor
	}
	if len(seen) != 250 {
		t.Fatalf("saw %d invoices, want 250", len(seen))
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] >= seen[i-1] {
			t.Fatalf("not strictly newest first at %d: %s after %s", i, seen[i], seen[i-1])
		}
	}

	// Filters.
	_, resp := s.send(t, c.Key, "GET", "/v1/invoices?customer_id="+other+"&status=draft&document_type=invoice", "", nil)
	var body struct {
		Data       []map[string]any `json:"data"`
		NextCursor *string          `json:"next_cursor"`
	}
	decodeJSON(t, resp, &body)
	if len(body.Data) != 1 || body.Data[0]["id"] != marked || body.NextCursor != nil {
		t.Errorf("filtered list = %v", body)
	}
	_, resp = s.send(t, c.Key, "GET", "/v1/invoices?status=issued", "", nil)
	decodeJSON(t, resp, &body)
	if len(body.Data) != 0 {
		t.Errorf("issued = %d invoices", len(body.Data))
	}

	// A cursor only works with the filters it was issued for.
	_, resp = s.send(t, c.Key, "GET", "/v1/invoices?limit=1", "", nil)
	decodeJSON(t, resp, &body)
	req, resp := s.send(t, c.Key, "GET", "/v1/invoices?limit=1&status=draft&cursor="+*body.NextCursor, "", nil)
	wantProblem(t, req, resp, http.StatusBadRequest, "bad_request")
}
