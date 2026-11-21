package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/admin"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/contracttest"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/store"
)

// Tests for issuing, crediting, correcting, and voiding (ADR 0016).
// "Criterion n" refers to the acceptance criteria listed there.

// A German customer with everything § 14 UStG needs.
const fullCustomerJSON = `{"kind":"business","name":"Beispiel Handels GmbH","address_line1":"Musterstraße 1",` +
	`"city":"Berlin","post_code":"10115","country_code":"DE"}`

// completeSeller fills the tenant's seller profile with synthetic data.
func (s dbServer) completeSeller(t *testing.T, c admin.Created) {
	t.Helper()
	s.exec(t, `
		update app.seller_profiles set legal_name = 'Muster Software GmbH', address_line1 = 'Hauptstraße 5',
		       city = 'München', post_code = '80331', country_code = 'DE', vat_id = 'DE000000001',
		       jp_registration_number = case (select jurisdiction from app.tenants where id = $1)
		                                when 'JP' then 'T0000000000001' end
		where tenant_id = $1`, c.TenantID)
}

// readyDraft creates a complete tenant setup and a German draft that can be
// issued, and returns its id.
func (s dbServer) readyDraft(t *testing.T, c admin.Created, cust string) string {
	t.Helper()
	return s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q,"supply_date":"2026-10-30","lines":%s}`, cust, deLines))["id"].(string)
}

func (s dbServer) issueReq(t *testing.T, key, id, idemKey string, headers map[string]string) (*http.Request, *http.Response) {
	t.Helper()
	h := map[string]string{"Idempotency-Key": idemKey, "Content-Type": ""}
	for k, v := range headers {
		h[k] = v
	}
	return s.send(t, key, "POST", "/v1/invoices/"+id+"/issue", "", h)
}

func (s dbServer) issueOK(t *testing.T, c admin.Created, id string) invoiceResp {
	t.Helper()
	req, resp := s.issueReq(t, c.Key, id, uuid.NewString(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("issue: status %d; %s; log: %s", resp.StatusCode, readAll(t, resp), s.logs)
	}
	contracttest.ValidateResponse(t, req, resp)
	var got invoiceResp
	decodeJSON(t, resp, &got)
	return got
}

func (s dbServer) voidReq(t *testing.T, key, id, reason string) (*http.Request, *http.Response) {
	t.Helper()
	return s.send(t, key, "POST", "/v1/invoices/"+id+"/void", fmt.Sprintf(`{"reason":%q}`, reason),
		map[string]string{"Idempotency-Key": uuid.NewString()})
}

func (s dbServer) events(t *testing.T, subject string) []string {
	t.Helper()
	rows, err := s.admin.Query(t.Context(), `select type from app.events where subject_id = $1 order by created_at, type`, subject)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for rows.Next() {
		var typ string
		if err := rows.Scan(&typ); err != nil {
			t.Fatal(err)
		}
		types = append(types, typ)
	}
	return types
}

func today(t *testing.T, tz string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	y, m, d := time.Now().In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Criterion 1.
func TestIssueGermanInvoice(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	s.completeSeller(t, c)
	cust := s.newCustomer(t, c, fullCustomerJSON)["id"].(string)
	id := s.readyDraft(t, c, cust)

	req, resp := s.issueReq(t, c.Key, id, "issue-1", map[string]string{"If-Match": `"1"`})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("issue: %d %s; log: %s", resp.StatusCode, readAll(t, resp), s.logs)
	}
	contracttest.ValidateResponse(t, req, resp)
	var got invoiceResp
	decodeJSON(t, resp, &got)
	issueDay := today(t, "Europe/Berlin")
	wantFields(t, got, map[string]any{
		"status": "issued", "number": "1", "issue_date": issueDay.Format(time.DateOnly),
		"tax_rounding": "half_away_from_zero", "retention_until": fmt.Sprintf("%d-12-31", issueDay.Year()+8),
		"tax_inclusive_total": "1173.19", "amount_paid": "0.00", "payment_status": "unpaid",
	})
	if got["issued_at"] == nil {
		t.Error("issued_at is null")
	}
	parties := got["parties"].([]any)
	if len(parties) != 2 {
		t.Fatalf("parties = %v", parties)
	}
	wantFields(t, parties[0].(map[string]any), map[string]any{"role": "seller", "name": "Muster Software GmbH", "vat_id": "DE000000001"})
	wantFields(t, parties[1].(map[string]any), map[string]any{"role": "buyer", "name": "Beispiel Handels GmbH", "city": "Berlin"})

	// The event: the snapshot the webhook will carry, with no personal data.
	if ev := s.events(t, id); !reflect.DeepEqual(ev, []string{"invoice.issued"}) {
		t.Errorf("events = %v", ev)
	}
	var payload map[string]any
	if err := s.admin.QueryRow(t.Context(), `select payload from app.events where subject_id = $1`, id).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	data := payload["data"].(map[string]any)
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"amount_due", "amount_paid", "currency", "customer_id", "document_type", "id", "issue_date",
		"number", "object", "preceding_invoice_id", "status", "tax_inclusive_total"}
	if !reflect.DeepEqual(keys, want) || payload["type"] != "invoice.issued" || data["number"] != "1" {
		t.Errorf("payload = %v", payload)
	}
	if n := s.count(t, `select count(*) from app.audit_log where action = 'invoice.issue' and subject_id = $1`, id); n != 1 {
		t.Errorf("%d audit entries", n)
	}

	// Later edits to the seller and customer do not change the issued copy.
	s.exec(t, `update app.seller_profiles set city = 'Hamburg' where tenant_id = $1`, c.TenantID)
	s.exec(t, `update app.customers set name = 'Renamed GmbH' where id = $1`, cust)
	_, resp = s.send(t, c.Key, "GET", "/v1/invoices/"+id, "", nil)
	var again invoiceResp
	decodeJSON(t, resp, &again)
	if !reflect.DeepEqual(again["parties"], got["parties"]) {
		t.Errorf("parties changed: %v", again["parties"])
	}

	// A replay returns the same document and uses no number.
	_, resp = s.issueReq(t, c.Key, id, "issue-1", map[string]string{"If-Match": `"1"`})
	decodeJSON(t, resp, &again)
	if resp.Header.Get("Idempotent-Replayed") != "true" || again["number"] != "1" {
		t.Errorf("replay: %v %v", resp.Header, again["number"])
	}
	if n := s.issueOK(t, c, s.readyDraft(t, c, cust))["number"]; n != "2" {
		t.Errorf("next number = %v, want 2", n)
	}
}

// Criterion 2: what each jurisdiction needs, all listed at once; the same
// key succeeds once the data is complete, and the failure used no number.
func TestIssueIncomplete(t *testing.T) {
	s := newDBServer(t)
	de := s.tenant(t, "DE")
	cust := s.customerID(t, de) // city and post code, no street
	id := s.newInvoice(t, de, fmt.Sprintf(`{"customer_id":%q}`, cust))["id"].(string)

	req, resp := s.issueReq(t, de.Key, id, "retry-me", nil)
	body := readAll(t, resp)
	wantProblem(t, req, resp, http.StatusConflict, "invoice_incomplete")
	wantErrorPointers(t, body, map[string]string{
		"/seller_profile/legal_name": "required", "/seller_profile/address_line1": "required",
		"/seller_profile/city": "required", "/seller_profile/post_code": "required",
		"/seller_profile/country_code": "required", "/seller_profile/vat_id": "required",
		"/customer/address_line1": "required", "/supply_date": "required", "/lines": "required",
	})
	if n := s.count(t, `select count(*) from app.events where tenant_id = $1`, de.TenantID); n != 0 {
		t.Errorf("%d events after a failed issue", n)
	}

	s.completeSeller(t, de)
	s.exec(t, `update app.customers set address_line1 = 'Musterstraße 1' where id = $1`, cust)
	_, resp = s.patchInvoice(t, de.Key, id, `"1"`, `{"supply_period_start":"2026-10-01","supply_period_end":"2026-10-31","lines":`+deLines+`}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: %d", resp.StatusCode)
	}
	req, resp = s.issueReq(t, de.Key, id, "retry-me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry: %d %s", resp.StatusCode, readAll(t, resp))
	}
	contracttest.ValidateResponse(t, req, resp)
	var got invoiceResp
	decodeJSON(t, resp, &got)
	if got["number"] != "1" {
		t.Errorf("number = %v, want 1 (the failed issue must not use one)", got["number"])
	}

	// Japan needs the issuer's name and registration number, and a date.
	jp := s.tenant(t, "JP")
	jid := s.newInvoice(t, jp, fmt.Sprintf(`{"customer_id":%q,"lines":[{"name":"Tea","quantity":"1","unit_price":"1000","tax_rate":"8"}]}`, s.customerID(t, jp)))["id"].(string)
	req, resp = s.issueReq(t, jp.Key, jid, uuid.NewString(), nil)
	body = readAll(t, resp)
	wantProblem(t, req, resp, http.StatusConflict, "invoice_incomplete")
	wantErrorPointers(t, body, map[string]string{
		"/seller_profile/legal_name": "required", "/seller_profile/jp_registration_number": "required", "/supply_date": "required",
	})
}

func wantErrorPointers(t *testing.T, body []byte, want map[string]string) {
	t.Helper()
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
		t.Errorf("errors = %v\nwant     %v", got, want)
	}
}

// Criterion 3: concurrent issues on one series get 1 to n, no gaps; a series
// with a prefix pads the counter.
func TestIssueNumbersAreGapless(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	s.completeSeller(t, c)
	cust := s.newCustomer(t, c, fullCustomerJSON)["id"].(string)

	const n = 20
	ids := make([]string, n)
	for i := range ids {
		ids[i] = s.readyDraft(t, c, cust)
	}
	numbers := make([]string, n)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			_, resp := s.issueReq(t, c.Key, id, uuid.NewString(), nil)
			var got invoiceResp
			if resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&got) == nil {
				numbers[i], _ = got["number"].(string)
			}
		})
	}
	wg.Wait()
	sort.Slice(numbers, func(i, j int) bool {
		return len(numbers[i]) < len(numbers[j]) || (len(numbers[i]) == len(numbers[j]) && numbers[i] < numbers[j])
	})
	for i, got := range numbers {
		if got != fmt.Sprint(i+1) {
			t.Fatalf("numbers = %v, want 1 to %d", numbers, n)
		}
	}

	var series string
	if err := s.admin.QueryRow(t.Context(), `
		insert into app.number_series (tenant_id, code, prefix, padding) values ($1, 'y2026', 'INV-2026-', 4) returning id`,
		c.TenantID).Scan(&series); err != nil {
		t.Fatal(err)
	}
	id := s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q,"series_id":%q,"supply_date":"2026-10-30","lines":%s}`, cust, series, deLines))["id"].(string)
	if got := s.issueOK(t, c, id)["number"]; got != "INV-2026-0001" {
		t.Errorf("number = %v", got)
	}
}

// Criterion 4: Japan records the tenant's rounding, and the retention clock
// starts two months after the end of the fiscal year.
func TestIssueJapanese(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "JP")
	s.completeSeller(t, c)
	s.exec(t, `update app.tenants set fiscal_year_end_month = 3 where id = $1`, c.TenantID)
	id := s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q,"supply_date":"2026-10-30","lines":[`+
		`{"name":"Rice","quantity":"3","unit_price":"333","tax_rate":"10"},{"name":"Tea","quantity":"1","unit_price":"1234","tax_rate":"8"}]}`,
		s.customerID(t, c)))["id"].(string)

	got := s.issueOK(t, c, id)
	day := today(t, "Asia/Tokyo")
	fyEnd := day.Year()
	if day.Month() > time.March {
		fyEnd++
	}
	wantFields(t, got, map[string]any{
		"tax_rounding": "down", "tax_total": "197", "issue_date": day.Format(time.DateOnly),
		"retention_until": fmt.Sprintf("%d-05-31", fyEnd+7), // FY ends 31 Mar; +2 months = 31 May; +7 years
	})
}

func TestRetentionUntil(t *testing.T) {
	d := func(s string) time.Time { v, _ := time.Parse(time.DateOnly, s); return v }
	for _, tc := range []struct {
		jurisdiction string
		fyEnd        int16
		issued, want string
	}{
		{"DE", 12, "2026-01-01", "2034-12-31"},
		{"DE", 12, "2026-12-31", "2034-12-31"},
		{"JP", 3, "2026-03-31", "2033-05-31"},  // the period's last day
		{"JP", 3, "2026-04-01", "2034-05-31"},  // the next period
		{"JP", 12, "2026-11-21", "2034-02-28"}, // calendar year: deadline end of February
		{"JP", 12, "2029-06-01", "2037-02-28"},
		{"JP", 12, "2024-06-01", "2032-02-29"}, // leap year
		{"JP", 11, "2026-11-30", "2034-01-31"},
	} {
		ten := tenantRow(tc.jurisdiction, tc.fyEnd)
		if got := retentionUntil(ten, d(tc.issued)).Format(time.DateOnly); got != tc.want {
			t.Errorf("%s FY end %d, issued %s: %s, want %s", tc.jurisdiction, tc.fyEnd, tc.issued, got, tc.want)
		}
	}
}

func tenantRow(jurisdiction string, fyEnd int16) store.AppTenant {
	return store.AppTenant{Jurisdiction: jurisdiction, FiscalYearEndMonth: fyEnd}
}

// Criterion 5.
func TestIssuePreconditions(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	s.completeSeller(t, c)
	cust := s.newCustomer(t, c, fullCustomerJSON)["id"].(string)
	id := s.readyDraft(t, c, cust)

	req, resp := s.issueReq(t, c.Key, id, uuid.NewString(), map[string]string{"If-Match": `"9"`})
	wantProblem(t, req, resp, http.StatusPreconditionFailed, "precondition_failed")
	req, resp = s.issueReq(t, c.Key, uuid.NewString(), uuid.NewString(), nil)
	wantProblem(t, req, resp, http.StatusNotFound, "not_found")

	// A rate is checked against the final supply date.
	s.exec(t, `update app.invoices set supply_date = '2024-12-31' where id = $1`, id)
	req, resp = s.issueReq(t, c.Key, id, uuid.NewString(), nil)
	body := readAll(t, resp)
	wantProblem(t, req, resp, http.StatusConflict, "invoice_incomplete")
	wantErrorPointers(t, body, map[string]string{"/lines/0/tax_rate": "rate_not_valid", "/lines/1/tax_rate": "rate_not_valid"})

	s.exec(t, `update app.invoices set supply_date = '2026-10-30' where id = $1`, id)

	// An empty VAT ID is no VAT ID (§ 14 Abs. 4 Nr. 2 UStG).
	s.exec(t, `update app.seller_profiles set vat_id = '', tax_number = null where tenant_id = $1`, c.TenantID)
	req, resp = s.issueReq(t, c.Key, id, uuid.NewString(), nil)
	body = readAll(t, resp)
	wantProblem(t, req, resp, http.StatusConflict, "invoice_incomplete")
	wantErrorPointers(t, body, map[string]string{"/seller_profile/vat_id": "required"})
	s.exec(t, `update app.seller_profiles set vat_id = null, tax_number = '143/000/00000' where tenant_id = $1`, c.TenantID)

	// Many clients send a JSON Content-Type even with no body.
	req, resp = s.issueReq(t, c.Key, id, uuid.NewString(), map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("issue with an empty JSON body: %d %s", resp.StatusCode, readAll(t, resp))
	}
	contracttest.ValidateResponse(t, req, resp)
	req, resp = s.issueReq(t, c.Key, id, uuid.NewString(), nil)
	wantProblem(t, req, resp, http.StatusConflict, "invoice_not_draft")
}

// Criterion 6.
func TestIssueCreditNotes(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	s.completeSeller(t, c)
	cust := s.newCustomer(t, c, fullCustomerJSON)["id"].(string)
	orig := s.issueOK(t, c, s.readyDraft(t, c, cust))["id"].(string) // 1173.19

	credit := func(price string) string {
		return s.newInvoice(t, c, fmt.Sprintf(`{"document_type":"credit_note","preceding_invoice_id":%q,"supply_date":"2026-10-30",`+
			`"lines":[{"name":"Discount","quantity":"1","unit_price":%q,"tax_rate":"19"}]}`, orig, price))["id"].(string)
	}
	first := credit("50.00") // 59.50
	tooMuch := credit("936.00")
	got := s.issueOK(t, c, first)
	wantFields(t, got, map[string]any{"document_type": "credit_note", "number": "2", "payment_status": nil, "tax_inclusive_total": "59.50"})
	if ev := s.events(t, first); !reflect.DeepEqual(ev, []string{"credit_note.issued"}) {
		t.Errorf("events = %v", ev)
	}

	// 59.50 + 1113.84 > 1173.19.
	req, resp := s.issueReq(t, c.Key, tooMuch, uuid.NewString(), nil)
	wantProblem(t, req, resp, http.StatusConflict, "credit_exceeds_invoice")

	// Exactly the rest: 935.87 net + 177.82 tax = 1113.69.
	got = s.issueOK(t, c, credit("935.87"))
	wantFields(t, got, map[string]any{"tax_inclusive_total": "1113.69"})

	// The original stays issued.
	_, resp = s.send(t, c.Key, "GET", "/v1/invoices/"+orig, "", nil)
	decodeJSON(t, resp, &got)
	wantFields(t, got, map[string]any{"status": "issued"})
}

// Criterion 7.
func TestIssueCorrection(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	s.completeSeller(t, c)
	cust := s.newCustomer(t, c, fullCustomerJSON)["id"].(string)
	orig := s.issueOK(t, c, s.readyDraft(t, c, cust))["id"].(string)

	correction := func(of string) string {
		return s.newInvoice(t, c, fmt.Sprintf(`{"document_type":"corrected_invoice","preceding_invoice_id":%q,"supply_date":"2026-10-30",`+
			`"lines":[{"name":"Consulting, October (corrected hours)","quantity":"8","unit_code":"HUR","unit_price":"95.00","tax_rate":"19"}]}`, of))["id"].(string)
	}
	first, second := correction(orig), correction(orig)
	got := s.issueOK(t, c, first)
	wantFields(t, got, map[string]any{"document_type": "corrected_invoice", "preceding_invoice_id": orig, "status": "issued", "payment_status": "unpaid"})

	_, resp := s.send(t, c.Key, "GET", "/v1/invoices/"+orig, "", nil)
	decodeJSON(t, resp, &got)
	if got["status"] != "corrected" || got["corrected_at"] == nil {
		t.Errorf("original = %v, corrected_at %v", got["status"], got["corrected_at"])
	}
	if ev := s.events(t, orig); !reflect.DeepEqual(ev, []string{"invoice.issued", "invoice.corrected"}) {
		t.Errorf("original events = %v", ev)
	}
	if ev := s.events(t, first); !reflect.DeepEqual(ev, []string{"invoice.issued"}) {
		t.Errorf("correction events = %v", ev)
	}

	req, resp := s.issueReq(t, c.Key, second, uuid.NewString(), nil)
	wantProblem(t, req, resp, http.StatusConflict, "correction_not_allowed")

	// A corrected invoice can itself be corrected.
	s.issueOK(t, c, correction(first))

	// An invoice with a credit note or a payment cannot be corrected yet.
	credited := s.issueOK(t, c, s.readyDraft(t, c, cust))["id"].(string)
	s.issueOK(t, c, s.newInvoice(t, c, fmt.Sprintf(`{"document_type":"credit_note","preceding_invoice_id":%q,"supply_date":"2026-10-30",`+
		`"lines":[{"name":"Discount","quantity":"1","unit_price":"1","tax_rate":"19"}]}`, credited))["id"].(string))
	req, resp = s.issueReq(t, c.Key, correction(credited), uuid.NewString(), nil)
	wantProblem(t, req, resp, http.StatusConflict, "correction_not_allowed")

	paid := s.issueOK(t, c, s.readyDraft(t, c, cust))["id"].(string)
	s.exec(t, `insert into app.payments (tenant_id, invoice_id, amount, paid_on) values ($1, $2, 100, current_date)`, c.TenantID, paid)
	req, resp = s.issueReq(t, c.Key, correction(paid), uuid.NewString(), nil)
	wantProblem(t, req, resp, http.StatusConflict, "correction_not_allowed")
}

// Criterion 8.
func TestVoidInvoice(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	s.completeSeller(t, c)
	cust := s.newCustomer(t, c, fullCustomerJSON)["id"].(string)
	orig := s.issueOK(t, c, s.readyDraft(t, c, cust))
	id := orig["id"].(string)

	// The credit note names the parties of the invoice, not today's.
	s.exec(t, `update app.customers set city = 'Köln' where id = $1`, cust)
	s.exec(t, `update app.seller_profiles set legal_name = null where tenant_id = $1`, c.TenantID)

	req, resp := s.voidReq(t, c.Key, id, "Sent to the wrong customer")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("void: %d %s; log: %s", resp.StatusCode, readAll(t, resp), s.logs)
	}
	contracttest.ValidateResponse(t, req, resp)
	var got struct {
		Voided     invoiceResp `json:"voided_invoice"`
		CreditNote invoiceResp `json:"credit_note"`
	}
	decodeJSON(t, resp, &got)
	wantFields(t, got.Voided, map[string]any{"status": "voided", "void_reason": "Sent to the wrong customer", "number": "1"})
	if got.Voided["voided_at"] == nil {
		t.Error("voided_at is null")
	}
	cn := got.CreditNote
	wantFields(t, cn, map[string]any{
		"document_type": "credit_note", "status": "issued", "number": "2", "preceding_invoice_id": id,
		"customer_id": cust, "tax_inclusive_total": "1173.19", "tax_total": "183.29",
		"note": "Voids invoice 1: Sent to the wrong customer",
	})
	if !reflect.DeepEqual(cn["parties"], orig["parties"]) {
		t.Errorf("credit note parties = %v\nwant %v", cn["parties"], orig["parties"])
	}
	if !reflect.DeepEqual(cn["tax_breakdown"], orig["tax_breakdown"]) || len(cn["lines"].([]any)) != 2 {
		t.Errorf("credit note lines or breakdown differ: %v", cn["tax_breakdown"])
	}
	if ev := s.events(t, id); !reflect.DeepEqual(ev, []string{"invoice.issued", "invoice.voided"}) {
		t.Errorf("original events = %v", ev)
	}
	if ev := s.events(t, cn["id"].(string)); !reflect.DeepEqual(ev, []string{"credit_note.issued"}) {
		t.Errorf("credit note events = %v", ev)
	}

	req, resp = s.voidReq(t, c.Key, id, "again")
	wantProblem(t, req, resp, http.StatusConflict, "invoice_not_issued")
	req, resp = s.voidReq(t, c.Key, cn["id"].(string), "a credit note")
	wantProblem(t, req, resp, http.StatusConflict, "invoice_not_issued")
	draft := s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q}`, cust))["id"].(string)
	req, resp = s.voidReq(t, c.Key, draft, "a draft")
	wantProblem(t, req, resp, http.StatusConflict, "invoice_not_issued")
	req, resp = s.voidReq(t, c.Key, uuid.NewString(), "unknown")
	wantProblem(t, req, resp, http.StatusNotFound, "not_found")

	// An invoice with a partial credit note is credited, not voided.
	s.completeSeller(t, c)
	partly := s.issueOK(t, c, s.readyDraft(t, c, cust))["id"].(string)
	s.issueOK(t, c, s.newInvoice(t, c, fmt.Sprintf(`{"document_type":"credit_note","preceding_invoice_id":%q,"supply_date":"2026-10-30",`+
		`"lines":[{"name":"Discount","quantity":"1","unit_price":"1","tax_rate":"19"}]}`, partly))["id"].(string))
	req, resp = s.voidReq(t, c.Key, partly, "too late")
	wantProblem(t, req, resp, http.StatusConflict, "credit_exceeds_invoice")
}

// Criterion 8: the credit note uses the rounding the invoice was issued
// with, so it cancels it exactly even after the tenant changed its mode.
func TestVoidKeepsOriginalRounding(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "JP") // down
	s.completeSeller(t, c)
	id := s.issueOK(t, c, s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q,"supply_date":"2026-10-30","lines":[`+
		`{"name":"Rice","quantity":"3","unit_price":"333","tax_rate":"10"}]}`, s.customerID(t, c)))["id"].(string))["id"].(string)
	s.exec(t, `update app.tenants set jp_tax_rounding = 'up' where id = $1`, c.TenantID)

	_, resp := s.voidReq(t, c.Key, id, "duplicate")
	var got struct {
		CreditNote invoiceResp `json:"credit_note"`
	}
	decodeJSON(t, resp, &got)
	wantFields(t, got.CreditNote, map[string]any{"tax_total": "99", "tax_inclusive_total": "1098", "tax_rounding": "down"})
}

// Criterion 9: the outbox. One delivery and one queue message per subscribed,
// enabled endpoint, carrying only IDs; nothing when the issue fails.
func TestIssueFansOutToWebhookEndpoints(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	s.completeSeller(t, c)
	cust := s.newCustomer(t, c, fullCustomerJSON)["id"].(string)
	var all string
	if err := s.admin.QueryRow(t.Context(), `
		insert into app.webhook_endpoints (tenant_id, url, secret_ciphertext, secret_key_id, event_types, enabled)
		values ($1, 'https://example.com/all', '\x00', 'test', '{}', true),
		       ($1, 'https://example.com/voids', '\x00', 'test', '{invoice.voided}', true),
		       ($1, 'https://example.com/off', '\x00', 'test', '{}', false)
		returning id`, c.TenantID).Scan(&all); err != nil {
		t.Fatal(err)
	}

	incomplete := s.newInvoice(t, c, fmt.Sprintf(`{"customer_id":%q}`, cust))["id"].(string)
	_, resp := s.issueReq(t, c.Key, incomplete, uuid.NewString(), nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("incomplete issue: %d", resp.StatusCode)
	}
	queued := func() int {
		return s.count(t, `select count(*) from pgmq.q_webhook_deliveries where message->>'tenant_id' = $1`, c.TenantID.String())
	}
	if n := queued(); n != 0 {
		t.Fatalf("%d messages after a failed issue", n)
	}

	id := s.issueOK(t, c, s.readyDraft(t, c, cust))["id"].(string)
	var endpoint string
	if err := s.admin.QueryRow(t.Context(), `
		select d.endpoint_id from app.webhook_deliveries d join app.events e on e.id = d.event_id
		where e.subject_id = $1 and d.status = 'pending' and d.next_attempt_at is not null`, id).Scan(&endpoint); err != nil {
		t.Fatalf("one pending delivery: %v", err)
	}
	if endpoint != all {
		t.Errorf("delivered to %s, want the all-events endpoint", endpoint)
	}
	var msg map[string]any
	if err := s.admin.QueryRow(t.Context(), `select message from pgmq.q_webhook_deliveries where message->>'tenant_id' = $1`,
		c.TenantID.String()).Scan(&msg); err != nil {
		t.Fatalf("one message: %v", err)
	}
	if len(msg) != 2 || msg["delivery_id"] == nil {
		t.Errorf("message = %v, want only delivery_id and tenant_id", msg)
	}

	// Voiding reaches both enabled endpoints.
	_, resp = s.voidReq(t, c.Key, id, "test")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("void: %d", resp.StatusCode)
	}
	if n := queued(); n != 4 { // issued: 1; credit_note.issued: 1; invoice.voided: 2
		t.Errorf("%d messages, want 4", n)
	}
}

// A POST with no body at all passes the body checks (issue has none), but an
// operation whose body is required still rejects it.
func TestPostWithoutBody(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	req, resp := s.send(t, c.Key, "POST", "/v1/invoices", "", map[string]string{"Idempotency-Key": "k", "Content-Type": ""})
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("status = %d, want a 4xx; %s", resp.StatusCode, readAll(t, resp))
	}
	contracttest.ValidateResponse(t, req, resp)
	if n := s.count(t, `select count(*) from app.invoices where tenant_id = $1`, c.TenantID); n != 0 {
		t.Errorf("%d invoices", n)
	}
}
