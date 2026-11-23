package httpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/admin"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/contracttest"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/dbtest"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/invoicepdf"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/objstoretest"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/typsttest"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/worker"
)

// Tests for rendering and listing PDFs (ADR 0017). "Criterion n" refers to
// the acceptance criteria listed there. They need the database, Storage, and
// Typst; each one is skipped locally, and fails in CI, when it is missing.

const testDrainToken = "test-drain-token-0123456789abcdef"

// newFileServer is newDBServer with a renderer, Storage, and the drain
// token. typst overrides the binary, to make renders fail.
func newFileServer(t *testing.T, typst string) dbServer {
	t.Helper()
	if typst == "" {
		typst = typsttest.Path(t)
	}
	r, err := invoicepdf.New(invoicepdf.Options{TypstPath: typst})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	logs := &bytes.Buffer{}
	s := dbServer{
		h: New(Config{
			Logger: slog.New(slog.NewTextHandler(logs, nil)), DB: dbtest.APIPool(t),
			Renderer: r, Files: objstoretest.Store(t), DrainToken: testDrainToken,
		}),
		admin: dbtest.AdminPool(t),
		logs:  logs,
	}
	// Messages left by other tests and earlier runs would make drains slow
	// and their counts unpredictable.
	s.exec(t, `select pgmq.purge_queue('render_pdf')`)
	return s
}

func (s dbServer) renderMessages(t *testing.T, invoiceID string) int {
	t.Helper()
	return s.count(t, `select count(*) from pgmq.q_render_pdf where message->>'invoice_id' = $1`, invoiceID)
}

type drainReport map[string]worker.Counts

func (s dbServer) drainReq(t *testing.T, auth string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("POST", "/internal/queue/drain", strings.NewReader("{}"))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec.Result()
}

func (s dbServer) drain(t *testing.T) drainReport {
	t.Helper()
	resp := s.drainReq(t, "Bearer "+testDrainToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("drain: %d %s; log: %s", resp.StatusCode, readAll(t, resp), s.logs)
	}
	var got struct {
		Queues drainReport `json:"queues"`
	}
	decodeJSON(t, resp, &got)
	return got.Queues
}

type fileResp struct {
	Kind         string    `json:"kind"`
	ContentType  string    `json:"content_type"`
	ByteSize     int64     `json:"byte_size"`
	SHA256       string    `json:"sha256"`
	URL          string    `json:"url"`
	URLExpiresAt time.Time `json:"url_expires_at"`
}

func (s dbServer) files(t *testing.T, key, id string) []fileResp {
	t.Helper()
	req, resp := s.do(t, key, "GET", "/v1/invoices/"+id+"/files", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("files: %d %s; log: %s", resp.StatusCode, readAll(t, resp), s.logs)
	}
	contracttest.ValidateResponse(t, req, resp)
	var got struct {
		Data []fileResp `json:"data"`
	}
	decodeJSON(t, resp, &got)
	return got.Data
}

func download(t *testing.T, u string) []byte {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: %d %s", resp.StatusCode, b)
	}
	return b
}

// issuedGerman returns a tenant and one issued German invoice.
func (s dbServer) issuedGerman(t *testing.T) (admin.Created, string, string) {
	t.Helper()
	c := s.tenant(t, "DE")
	s.completeSeller(t, c)
	cust := s.newCustomer(t, c, fullCustomerJSON)["id"].(string)
	id := s.issueOK(t, c, s.readyDraft(t, c, cust))["id"].(string)
	return c, cust, id
}

// Criterion 1.
func TestIssuingEnqueuesOneRender(t *testing.T) {
	s := newFileServer(t, "")
	c, cust, id := s.issuedGerman(t)
	if n := s.renderMessages(t, id); n != 1 {
		t.Errorf("invoice: %d render messages, want 1", n)
	}

	credit := s.newInvoice(t, c, fmt.Sprintf(`{"document_type":"credit_note","preceding_invoice_id":%q,"supply_date":"2026-10-30",`+
		`"lines":[{"name":"Discount","quantity":"1","unit_price":"50.00","tax_rate":"19"}]}`, id))["id"].(string)
	s.issueOK(t, c, credit)
	if n := s.renderMessages(t, credit); n != 1 {
		t.Errorf("credit note: %d render messages, want 1", n)
	}

	other := s.issueOK(t, c, s.readyDraft(t, c, cust))["id"].(string)
	correction := s.newInvoice(t, c, fmt.Sprintf(`{"document_type":"corrected_invoice","preceding_invoice_id":%q,"supply_date":"2026-10-30",`+
		`"lines":%s}`, other, deLines))["id"].(string)
	s.issueOK(t, c, correction)
	if n := s.renderMessages(t, correction); n != 1 {
		t.Errorf("corrected invoice: %d render messages, want 1", n)
	}

	third := s.issueOK(t, c, s.readyDraft(t, c, cust))["id"].(string)
	req, resp := s.voidReq(t, c.Key, third, "Wrong customer")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("void: %d %s", resp.StatusCode, readAll(t, resp))
	}
	contracttest.ValidateResponse(t, req, resp)
	var voided struct {
		CreditNote invoiceResp `json:"credit_note"`
	}
	decodeJSON(t, resp, &voided)
	if n := s.renderMessages(t, voided.CreditNote["id"].(string)); n != 1 {
		t.Errorf("void credit note: %d render messages, want 1", n)
	}
	if n := s.renderMessages(t, third); n != 1 {
		t.Errorf("voided invoice: %d render messages, want 1 (its own, from issue)", n)
	}

	// A failed issue enqueues nothing.
	s.exec(t, `update app.seller_profiles set legal_name = null where tenant_id = $1`, c.TenantID)
	draft := s.readyDraft(t, c, cust)
	_, resp = s.issueReq(t, c.Key, draft, uuid.NewString(), nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("issue incomplete: %d, want 409", resp.StatusCode)
	}
	if n := s.renderMessages(t, draft); n != 0 {
		t.Errorf("failed issue: %d render messages, want 0", n)
	}
}

// Criteria 2 and 3.
func TestDrainRendersAndFilesListsThePDF(t *testing.T) {
	s := newFileServer(t, "")
	c, cust, id := s.issuedGerman(t)
	draft := s.readyDraft(t, c, cust)

	// Before the drain, and for a draft: an empty list.
	if got := s.files(t, c.Key, id); len(got) != 0 {
		t.Errorf("files before drain = %v", got)
	}
	if got := s.files(t, c.Key, draft); len(got) != 0 {
		t.Errorf("files of a draft = %v", got)
	}

	report := s.drain(t)
	if r := report["render_pdf"]; r.Done != 1 || r.Failed != 0 || r.GivenUp != 0 {
		t.Fatalf("drain report = %+v; log: %s", report, s.logs)
	}
	if n := s.renderMessages(t, id); n != 0 {
		t.Errorf("%d messages left after a successful render", n)
	}

	before := time.Now()
	got := s.files(t, c.Key, id)
	if len(got) != 1 {
		t.Fatalf("files = %v", got)
	}
	f := got[0]
	if f.Kind != "pdf" || f.ContentType != "application/pdf" {
		t.Errorf("file = %+v", f)
	}
	body := download(t, f.URL)
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != f.SHA256 || int64(len(body)) != f.ByteSize {
		t.Errorf("downloaded %d bytes with SHA-256 %x; listed %d, %s", len(body), sum, f.ByteSize, f.SHA256)
	}
	if !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Error("download is not a PDF")
	}
	if d := f.URLExpiresAt.Sub(before); d < 4*time.Minute+50*time.Second || d > 5*time.Minute+10*time.Second {
		t.Errorf("url_expires_at is %v after the request, want 5 minutes", d)
	}
	if u, _ := url.Parse(f.URL); u.Query().Get("X-Amz-Expires") != "300" {
		t.Errorf("URL expires after %q seconds, want 300", u.Query().Get("X-Amz-Expires"))
	}

	// Another tenant cannot see the document.
	other := s.tenant(t, "DE")
	req, resp := s.do(t, other.Key, "GET", "/v1/invoices/"+id+"/files", "")
	wantProblem(t, req, resp, http.StatusNotFound, "not_found")
}

// Criterion 4.
func TestRenderIsNeverRepeated(t *testing.T) {
	s := newFileServer(t, "")
	c, _, id := s.issuedGerman(t)
	s.drain(t)
	first := s.files(t, c.Key, id)

	// A second message for the same document, as after a crash between
	// upload and archive, renders nothing and leaves one row.
	s.exec(t, `select pgmq.send('render_pdf', jsonb_build_object('tenant_id', $1::text, 'invoice_id', $2::text))`, c.TenantID, id)
	if r := s.drain(t)["render_pdf"]; r.Done != 1 {
		t.Errorf("second drain = %+v", r)
	}
	if !strings.Contains(s.logs.String(), "already rendered") {
		t.Errorf("no 'already rendered' log; log: %s", s.logs)
	}
	second := s.files(t, c.Key, id)
	if len(second) != 1 || second[0].SHA256 != first[0].SHA256 {
		t.Errorf("files after a repeated message = %+v, want %+v", second, first)
	}
	if n := s.count(t, `select count(*) from app.invoice_files where invoice_id = $1`, id); n != 1 {
		t.Errorf("%d rows", n)
	}
}

// Criterion 5.
func TestFailingRenderIsGivenUpAfterFiveAttempts(t *testing.T) {
	s := newFileServer(t, filepath.Join(t.TempDir(), "no-typst"))
	c, _, id := s.issuedGerman(t)
	for i := 1; i <= 5; i++ {
		if i > 1 {
			// The minute of visibility timeout passes.
			s.exec(t, `update pgmq.q_render_pdf set vt = now() where message->>'invoice_id' = $1`, id)
		}
		r := s.drain(t)["render_pdf"]
		wantGivenUp := 0
		if i == 5 {
			wantGivenUp = 1
		}
		if r.Failed != 1 || r.GivenUp != wantGivenUp || r.Done != 0 {
			t.Fatalf("drain %d = %+v; log: %s", i, r, s.logs)
		}
	}
	if n := s.renderMessages(t, id); n != 0 {
		t.Errorf("%d messages left after giving up", n)
	}
	if n := s.count(t, `select count(*) from pgmq.a_render_pdf where message->>'invoice_id' = $1`, id); n != 1 {
		t.Errorf("%d archived messages, want 1", n)
	}
	s.exec(t, `update pgmq.q_render_pdf set vt = now()`)
	if r := s.drain(t)["render_pdf"]; r != (worker.Counts{}) {
		t.Errorf("drain after giving up = %+v", r)
	}
	if got := s.files(t, c.Key, id); len(got) != 0 {
		t.Errorf("files = %v", got)
	}
}

// Criterion 5: one bad message does not hold up the others.
func TestFailingMessageDoesNotBlockOthers(t *testing.T) {
	s := newFileServer(t, "")
	c, _, id := s.issuedGerman(t)
	// A message for a tenant that does not exist fails at every attempt;
	// it is sent first, so it is read before the good one.
	s.exec(t, `select pgmq.purge_queue('render_pdf')`)
	s.exec(t, `select pgmq.send('render_pdf', jsonb_build_object('tenant_id', 'not-a-uuid', 'invoice_id', $1::text))`, id)
	s.exec(t, `select pgmq.send('render_pdf', jsonb_build_object('tenant_id', $1::text, 'invoice_id', $2::text))`, c.TenantID, id)
	r := s.drain(t)["render_pdf"]
	if r.Done != 1 || r.Failed != 1 {
		t.Fatalf("drain = %+v; log: %s", r, s.logs)
	}
	if got := s.files(t, c.Key, id); len(got) != 1 {
		t.Errorf("files = %v", got)
	}
}

// Criterion 6.
func TestDrainNeedsTheToken(t *testing.T) {
	s := newFileServer(t, "")
	for name, auth := range map[string]string{
		"none":         "",
		"wrong":        "Bearer " + strings.Repeat("x", len(testDrainToken)),
		"prefix":       "Bearer " + testDrainToken[:10],
		"basic scheme": "Basic " + testDrainToken,
	} {
		resp := s.drainReq(t, auth)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		}
		var p map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&p)
		if p["code"] != "unauthorized" {
			t.Errorf("%s: problem = %v", name, p)
		}
	}

	// Without a configured token nothing gets in, not even an empty bearer.
	logs := &bytes.Buffer{}
	unset := dbServer{h: New(Config{Logger: slog.New(slog.NewTextHandler(logs, nil)), DB: dbtest.APIPool(t)})}
	for _, auth := range []string{"", "Bearer ", "Bearer " + testDrainToken} {
		if resp := unset.drainReq(t, auth); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token unset, %q: status %d, want 401", auth, resp.StatusCode)
		}
	}
}

// Criterion 4: a crash after the upload but before the row. The insert is
// made to fail; the retry stores the same object and inserts the row once.
func TestRetryAfterUploadWithoutRow(t *testing.T) {
	s := newFileServer(t, "")
	c, _, id := s.issuedGerman(t)
	s.exec(t, `revoke insert on app.invoice_files from jade_api`)
	// t.Context is canceled by the time cleanups run.
	t.Cleanup(func() { _, _ = s.admin.Exec(context.Background(), `grant insert on app.invoice_files to jade_api`) })
	if r := s.drain(t)["render_pdf"]; r.Failed != 1 {
		t.Fatalf("drain with the insert failing = %+v", r)
	}
	s.exec(t, `grant insert on app.invoice_files to jade_api`)
	s.exec(t, `update pgmq.q_render_pdf set vt = now() where message->>'invoice_id' = $1`, id)
	if r := s.drain(t)["render_pdf"]; r.Done != 1 {
		t.Fatalf("retry = %+v; log: %s", r, s.logs)
	}
	got := s.files(t, c.Key, id)
	if len(got) != 1 {
		t.Fatalf("files = %v", got)
	}
	sum := sha256.Sum256(download(t, got[0].URL))
	if hex.EncodeToString(sum[:]) != got[0].SHA256 {
		t.Error("stored object does not match the row")
	}
}

// Criterion 5: a message read more often than allowed, because an attempt
// died before it was counted, is given up without another render.
func TestOverReadMessageIsGivenUpUnhandled(t *testing.T) {
	s := newFileServer(t, "")
	c, _, id := s.issuedGerman(t)
	s.exec(t, `update pgmq.q_render_pdf set read_ct = 5 where message->>'invoice_id' = $1`, id)
	if r := s.drain(t)["render_pdf"]; r.GivenUp != 1 || r.Done != 0 {
		t.Fatalf("drain = %+v", r)
	}
	if got := s.files(t, c.Key, id); len(got) != 0 {
		t.Errorf("rendered on the 6th read: %v", got)
	}
}

// D10: without Storage the render queue is not read, so its messages keep
// all their attempts until Storage is configured.
func TestMessagesWaitWithoutStorage(t *testing.T) {
	s := newFileServer(t, "")
	_, _, id := s.issuedGerman(t)
	logs := &bytes.Buffer{}
	r, err := invoicepdf.New(invoicepdf.Options{TypstPath: typsttest.Path(t)})
	if err != nil {
		t.Fatal(err)
	}
	bare := dbServer{h: New(Config{
		Logger: slog.New(slog.NewTextHandler(logs, nil)), DB: dbtest.APIPool(t), DrainToken: testDrainToken, Renderer: r,
	}), admin: s.admin, logs: logs}
	if r, ok := bare.drain(t)["render_pdf"]; ok {
		t.Errorf("render queue read without Storage: %+v", r)
	}
	if n := s.count(t, `select count(*) from pgmq.q_render_pdf where message->>'invoice_id' = $1 and read_ct = 0`, id); n != 1 {
		t.Errorf("%d unread messages, want 1", n)
	}
}

// D5: the scheduled function calls the drain endpoint only when Vault holds
// its URL and token and a render message is visible.
func TestScheduledDrainRequest(t *testing.T) {
	s := newFileServer(t, "")
	call := func() bool {
		var id *int64
		if err := s.admin.QueryRow(t.Context(), `select app.request_queue_drain()`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id != nil
	}
	deleteSecrets := func() {
		_, _ = s.admin.Exec(context.Background(), `delete from vault.secrets where name in ('drain_url', 'drain_token')`)
	}
	deleteSecrets()
	t.Cleanup(deleteSecrets)

	_, _, _ = s.issuedGerman(t)
	if call() {
		t.Error("called without Vault secrets")
	}
	s.exec(t, `select vault.create_secret('http://127.0.0.1:9/internal/queue/drain', 'drain_url')`)
	s.exec(t, `select vault.create_secret('test-token', 'drain_token')`)
	if !call() {
		t.Error("no call with secrets and a waiting message")
	}
	s.exec(t, `select pgmq.purge_queue('render_pdf')`)
	if call() {
		t.Error("called with an empty queue")
	}
}
