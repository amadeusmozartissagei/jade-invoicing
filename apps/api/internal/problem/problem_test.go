package problem

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each Kind must match its entry in docs/api/errors.md, which RFC 9457 §4
// requires as the problem type's documentation: a "### `code`" heading
// followed by "`status` · **Title.**".
func TestKindsMatchErrorsDoc(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "api", "errors.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(doc), "\r\n", "\n")
	for _, k := range []Kind{BadRequest, Unauthorized, Forbidden, NotFound, MethodNotAllowed, ContentTooLarge, UnsupportedMediaType, ValidationFailed, Internal} {
		want := fmt.Sprintf("### `%s`\n\n`%d` · **%s.**", k.Code, k.Status, k.Title)
		if !strings.Contains(text, want) {
			t.Errorf("errors.md has no entry %q", want)
		}
	}
}

func TestWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, New(NotFound, "req-1", "No such customer."))

	if rec.Code != 404 {
		t.Errorf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Errorf("Content-Type = %q", ct)
	}
	want := `{"code":"not_found","detail":"No such customer.","request_id":"req-1","status":404,"title":"Not found","type":"` + TypeBase + `not_found"}`
	if got := rec.Body.String(); got != want {
		t.Errorf("body =\n%s\nwant\n%s", got, want)
	}
}
