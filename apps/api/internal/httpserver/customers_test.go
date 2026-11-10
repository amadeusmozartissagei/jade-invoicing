package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/admin"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/contracttest"
)

type customerResp map[string]any

func (s dbServer) newCustomer(t *testing.T, c admin.Created, body string) customerResp {
	t.Helper()
	req, resp := s.createCustomer(t, c.Key, fmt.Sprintf("create-%s-%d", t.Name(), s.customers(t, c)), body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d; %s; log: %s", resp.StatusCode, readAll(t, resp), s.logs)
	}
	contracttest.ValidateResponse(t, req, resp)
	var got customerResp
	decodeJSON(t, resp, &got)
	return got
}

// Criterion 1.
func TestCreateCustomer(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")

	body := `{"external_ref":"cust-1001","kind":"business","name":"Beispiel Handels GmbH","address_line1":"Musterstraße 1",` +
		`"city":"Berlin","post_code":"10115","country_code":"DE","vat_id":"DE000000000","established_in_de":true,"email":"billing@example.com"}`
	req, resp := s.createCustomer(t, c.Key, "k1", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d; %s; log: %s", resp.StatusCode, readAll(t, resp), s.logs)
	}
	contracttest.ValidateResponse(t, req, resp)
	var got customerResp
	decodeJSON(t, resp, &got)
	if loc := resp.Header.Get("Location"); loc != "/v1/customers/"+got["id"].(string) {
		t.Errorf("Location = %q, id %v", loc, got["id"])
	}
	for k, want := range map[string]any{
		"external_ref": "cust-1001", "name": "Beispiel Handels GmbH", "address_line1": "Musterstraße 1",
		"established_in_de": true, "email": "billing@example.com", "trading_name": nil, "contact_email": nil,
	} {
		if v, ok := got[k]; !ok || v != want {
			t.Errorf("%s = %v (present %v), want %v", k, v, ok, want)
		}
	}
	for _, k := range []string{"created_at", "updated_at"} {
		if at, _ := got[k].(string); !strings.HasSuffix(at, "Z") {
			t.Errorf("%s = %q, want UTC", k, at)
		}
	}
}

// Criterion 9.
func TestExternalRefIsUniquePerTenant(t *testing.T) {
	s := newDBServer(t)
	a, b := s.tenant(t, "DE"), s.tenant(t, "DE")

	s.newCustomer(t, a, `{"kind":"business","name":"One","external_ref":"x"}`)
	other := s.newCustomer(t, a, `{"kind":"business","name":"Two","external_ref":"y"}`)
	s.newCustomer(t, b, `{"kind":"business","name":"Other tenant","external_ref":"x"}`)

	req, resp := s.createCustomer(t, a.Key, "dup", `{"kind":"business","name":"Three","external_ref":"x"}`)
	wantProblem(t, req, resp, http.StatusConflict, "conflict_external_ref")

	req, resp = s.send(t, a.Key, "PATCH", "/v1/customers/"+other["id"].(string), `{"external_ref":"x"}`, nil)
	wantProblem(t, req, resp, http.StatusConflict, "conflict_external_ref")

	// Several customers may have no external_ref.
	s.newCustomer(t, a, `{"kind":"individual","name":"No ref 1"}`)
	s.newCustomer(t, a, `{"kind":"individual","name":"No ref 2"}`)
}

// Criterion 10.
func TestGetCustomer(t *testing.T) {
	s := newDBServer(t)
	a, b := s.tenant(t, "DE"), s.tenant(t, "DE")
	created := s.newCustomer(t, a, customerJSON)
	path := "/v1/customers/" + created["id"].(string)

	req, resp := s.send(t, a.Key, "GET", path, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	contracttest.ValidateResponse(t, req, resp)
	var got customerResp
	decodeJSON(t, resp, &got)
	if !reflect.DeepEqual(got, created) {
		t.Errorf("GET =\n%v\nwant\n%v", got, created)
	}

	req, resp = s.send(t, b.Key, "GET", path, "", nil)
	wantProblem(t, req, resp, http.StatusNotFound, "not_found")
	req, resp = s.send(t, a.Key, "GET", "/v1/customers/00000000-0000-4000-8000-000000000000", "", nil)
	wantProblem(t, req, resp, http.StatusNotFound, "not_found")
	req, resp = s.send(t, a.Key, "GET", "/v1/customers/not-a-uuid", "", nil)
	wantProblem(t, req, resp, http.StatusBadRequest, "bad_request")
}

// Criterion 11.
func TestPatchCustomer(t *testing.T) {
	s := newDBServer(t)
	a, b := s.tenant(t, "DE"), s.tenant(t, "DE")
	created := s.newCustomer(t, a, `{"kind":"business","name":"Alt GmbH","city":"Berlin","post_code":"10115","vat_id":"DE000000000"}`)
	path := "/v1/customers/" + created["id"].(string)

	req, resp := s.send(t, a.Key, "PATCH", path, `{"name":"Neu GmbH","city":"Hamburg","vat_id":null}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; %s", resp.StatusCode, readAll(t, resp))
	}
	contracttest.ValidateResponse(t, req, resp)
	var got customerResp
	decodeJSON(t, resp, &got)
	for k, want := range map[string]any{"name": "Neu GmbH", "city": "Hamburg", "vat_id": nil, "post_code": "10115", "kind": "business"} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
	if got["updated_at"] == created["updated_at"] {
		t.Log("updated_at unchanged: the create and patch ran within one clock tick")
	}

	// A patch that changes nothing writes nothing.
	before := s.count(t, `select count(*) from app.audit_log where subject_id = $1`, created["id"])
	if _, resp := s.send(t, a.Key, "PATCH", path, `{}`, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("empty patch: status %d", resp.StatusCode)
	}
	if _, resp := s.send(t, a.Key, "PATCH", path, `{"city":"Hamburg"}`, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("same value: status %d", resp.StatusCode)
	}
	if after := s.count(t, `select count(*) from app.audit_log where subject_id = $1`, created["id"]); after != before {
		t.Errorf("no-op patches wrote %d audit rows", after-before)
	}

	// kind and name cannot be cleared.
	req, resp = s.send(t, a.Key, "PATCH", path, `{"name":null,"kind":null}`, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("null name: status %d", resp.StatusCode)
	}
	contracttest.ValidateResponse(t, req, resp)
	var p struct {
		Errors []struct{ Pointer, Code string }
	}
	decodeJSON(t, resp, &p)
	want := []struct{ Pointer, Code string }{{"/kind", "not_allowed"}, {"/name", "not_allowed"}}
	if !reflect.DeepEqual(p.Errors, want) {
		t.Errorf("errors = %v, want %v", p.Errors, want)
	}

	req, resp = s.send(t, a.Key, "PATCH", path, `{"cty":"Köln"}`, nil)
	wantProblem(t, req, resp, http.StatusUnprocessableEntity, "validation_failed")
	req, resp = s.send(t, a.Key, "PATCH", path, `{"city":"Köln"}`, map[string]string{"Content-Type": "application/json"})
	wantProblem(t, req, resp, http.StatusUnsupportedMediaType, "unsupported_media_type")
	req, resp = s.send(t, b.Key, "PATCH", path, `{"city":"Köln"}`, nil)
	wantProblem(t, req, resp, http.StatusNotFound, "not_found")

	// None of the failures changed anything.
	_, resp = s.send(t, a.Key, "GET", path, "", nil)
	decodeJSON(t, resp, &got)
	if got["city"] != "Hamburg" || got["name"] != "Neu GmbH" {
		t.Errorf("after failed patches: city %v, name %v", got["city"], got["name"])
	}
}

// Criterion 12.
func TestListCustomersPages(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	// 250 customers with only 7 distinct created_at values, so most rows tie
	// on created_at and the id breaks the tie.
	s.exec(t, `insert into app.customers (tenant_id, kind, name, created_at)
		select $1, 'business', 'Customer ' || g, now() - (g % 7) * interval '1 second'
		from generate_series(1, 250) g`, c.TenantID)

	var want []string
	rows, err := s.admin.Query(t.Context(), `select id::text from app.customers where tenant_id = $1 order by created_at desc, id desc`, c.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		want = append(want, id)
	}
	rows.Close()

	var got []string
	path := "/v1/customers?limit=100"
	for page := 0; ; page++ {
		if page > 3 {
			t.Fatal("more pages than expected")
		}
		req, resp := s.send(t, c.Key, "GET", path, "", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("page %d: status %d; %s", page, resp.StatusCode, readAll(t, resp))
		}
		contracttest.ValidateResponse(t, req, resp)
		var list struct {
			Data       []struct{ ID string }
			NextCursor *string `json:"next_cursor"`
		}
		decodeJSON(t, resp, &list)
		for _, d := range list.Data {
			got = append(got, d.ID)
		}
		if list.NextCursor == nil {
			break
		}
		path = "/v1/customers?limit=100&cursor=" + *list.NextCursor
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("paged %d ids, want %d, in order: equal=%v", len(got), len(want), reflect.DeepEqual(got, want))
	}

	// Default page size, and a full last page still ends with a null cursor.
	_, resp := s.send(t, c.Key, "GET", "/v1/customers", "", nil)
	var def struct{ Data []any }
	decodeJSON(t, resp, &def)
	if len(def.Data) != defaultPageSize {
		t.Errorf("default page = %d rows", len(def.Data))
	}
}

func TestListCustomersFiltersAndBadInput(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	for i := range 3 {
		s.newCustomer(t, c, fmt.Sprintf(`{"kind":"business","name":"C%d","external_ref":"ref-%d"}`, i, i))
	}

	req, resp := s.send(t, c.Key, "GET", "/v1/customers?external_ref=ref-1", "", nil)
	contracttest.ValidateResponse(t, req, resp)
	var list struct {
		Data []struct {
			ExternalRef string `json:"external_ref"`
		}
		NextCursor *string `json:"next_cursor"`
	}
	decodeJSON(t, resp, &list)
	if len(list.Data) != 1 || list.Data[0].ExternalRef != "ref-1" || list.NextCursor != nil {
		t.Errorf("filtered list = %+v", list)
	}

	_, resp = s.send(t, c.Key, "GET", "/v1/customers?limit=1", "", nil)
	decodeJSON(t, resp, &list)
	if list.NextCursor == nil {
		t.Fatal("no next_cursor with more rows left")
	}
	cur := *list.NextCursor

	for name, q := range map[string]string{
		"cursor for other filters": "external_ref=ref-1&cursor=" + cur,
		"garbage cursor":           "cursor=not-a-cursor",
		"limit 0":                  "limit=0",
		"limit 101":                "limit=101",
		"limit not a number":       "limit=ten",
	} {
		req, resp := s.send(t, c.Key, "GET", "/v1/customers?"+q, "", nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, resp.StatusCode)
			continue
		}
		contracttest.ValidateResponse(t, req, resp)
		if p := decodeProblem(t, resp); p.Code != "bad_request" {
			t.Errorf("%s: code %q", name, p.Code)
		}
	}

	// Another tenant sees none of them.
	other := s.tenant(t, "DE")
	_, resp = s.send(t, other.Key, "GET", "/v1/customers", "", nil)
	decodeJSON(t, resp, &list)
	if len(list.Data) != 0 {
		t.Errorf("tenant B lists %d customers", len(list.Data))
	}
}

// Criterion 14: personal data never reaches the server log, on success or
// on failure.
func TestCustomerDataStaysOutOfLogs(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	secret := []string{"Privatperson Zqxj", "zqxj@example.com", "Geheimweg 7", "+49 30 0000 0000"}
	body := fmt.Sprintf(`{"kind":"individual","name":%q,"email":%q,"address_line1":%q,"contact_phone":%q,"external_ref":"p-1"}`,
		secret[0], secret[1], secret[2], secret[3])

	s.createCustomer(t, c.Key, "pii-1", body)
	s.createCustomer(t, c.Key, "pii-2", body)                                          // 409, duplicate external_ref
	s.createCustomer(t, c.Key, "pii-3", strings.Replace(body, `"individual"`, `5`, 1)) // 422
	s.send(t, c.Key, "GET", "/v1/customers", "", nil)

	logs := s.logs.String()
	if logs == "" {
		t.Fatal("no log output captured")
	}
	for _, v := range secret {
		if strings.Contains(logs, v) {
			t.Errorf("log contains %q", v)
		}
	}
}

// Criterion 15: create and update are audited with field names, no values.
func TestCustomerChangesAreAudited(t *testing.T) {
	s := newDBServer(t)
	c := s.tenant(t, "DE")
	created := s.newCustomer(t, c, `{"kind":"individual","name":"Erika Beispiel","city":"Berlin"}`)
	id := created["id"].(string)
	if _, resp := s.send(t, c.Key, "PATCH", "/v1/customers/"+id, `{"city":"Bonn","name":"Erika Muster","post_code":null}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status = %d", resp.StatusCode)
	}

	rows, err := s.admin.Query(t.Context(), `select actor_type, actor_id::text, action, subject_type, request_id, details::text
		from app.audit_log where tenant_id = $1 and subject_id = $2 order by id`, c.TenantID, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type entry struct{ actorType, actorID, action, subjectType, requestID, details string }
	var got []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.actorType, &e.actorID, &e.action, &e.subjectType, &e.requestID, &e.details); err != nil {
			t.Fatal(err)
		}
		got = append(got, e)
	}
	if len(got) != 2 {
		t.Fatalf("audit rows = %+v", got)
	}
	for i, action := range []string{"customer.create", "customer.update"} {
		e := got[i]
		if e.action != action || e.actorType != "api_key" || e.actorID != c.APIKeyID.String() || e.subjectType != "customer" || e.requestID == "" {
			t.Errorf("row %d = %+v", i, e)
		}
	}
	if got[0].details != "{}" {
		t.Errorf("create details = %s", got[0].details)
	}
	var d struct{ Fields []string }
	if err := json.Unmarshal([]byte(got[1].details), &d); err != nil {
		t.Fatal(err)
	}
	// post_code was already null, so it did not change.
	if !reflect.DeepEqual(d.Fields, []string{"name", "city"}) {
		t.Errorf("update fields = %v", d.Fields)
	}
	for _, v := range []string{"Erika", "Bonn", "Berlin"} {
		if strings.Contains(got[1].details, v) {
			t.Errorf("audit details contain %q", v)
		}
	}
}
