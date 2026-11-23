package objstore_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/objstoretest"
)

func get(t *testing.T, u string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// Criterion 2 at this seam: a presigned URL downloads exactly what was put.
func TestPresignedURLDownloadsWhatWasPut(t *testing.T) {
	s := objstoretest.Store(t)
	ctx := context.Background()
	key := "test/" + uuid.NewString() + ".pdf"
	body := []byte("%PDF-1.7 synthetic")
	if err := s.Put(ctx, key, body, "application/pdf"); err != nil {
		t.Fatal(err)
	}
	u, err := s.PresignGet(ctx, key, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if q, _ := url.Parse(u); q.Query().Get("X-Amz-Expires") != "300" {
		t.Errorf("X-Amz-Expires = %q, want 300", q.Query().Get("X-Amz-Expires"))
	}
	code, got := get(t, u)
	if code != http.StatusOK || !bytes.Equal(got, body) {
		t.Fatalf("GET presigned = %d %q, want 200 %q", code, got, body)
	}
}

// Criterion 11: the bucket is private; only a valid signature downloads.
func TestObjectsNeedAValidSignature(t *testing.T) {
	cfg := objstoretest.Config(t)
	s := objstoretest.Store(t)
	ctx := context.Background()
	key := "test/" + uuid.NewString() + ".pdf"
	if err := s.Put(ctx, key, []byte("%PDF-1.7 private"), "application/pdf"); err != nil {
		t.Fatal(err)
	}
	u, err := s.PresignGet(ctx, key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(u)
	q := parsed.Query()
	sig := q.Get("X-Amz-Signature")
	q.Set("X-Amz-Signature", strings.Repeat("0", len(sig)))
	parsed.RawQuery = q.Encode()

	storageAPI := strings.TrimSuffix(cfg.Endpoint, "/s3")
	for name, u := range map[string]string{
		"unsigned S3":     cfg.Endpoint + "/" + cfg.Bucket + "/" + key,
		"wrong signature": parsed.String(),
		"public URL":      storageAPI + "/object/public/" + cfg.Bucket + "/" + key,
		"no auth REST":    storageAPI + "/object/" + cfg.Bucket + "/" + key,
	} {
		if code, _ := get(t, u); code < 400 {
			t.Errorf("%s: GET = %d, want an error", name, code)
		}
	}
}
