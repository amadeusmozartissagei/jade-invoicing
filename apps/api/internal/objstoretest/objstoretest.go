// Package objstoretest connects tests to the local Supabase Storage, like
// dbtest does for Postgres (ADR 0017, D7).
package objstoretest

import (
	"os"
	"testing"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/objstore"
)

// Config reads TEST_S3_ENDPOINT, TEST_S3_REGION, TEST_S3_ACCESS_KEY_ID and
// TEST_S3_SECRET_ACCESS_KEY (`supabase status` prints the local values).
// Without them the test is skipped locally but fails in CI.
func Config(t *testing.T) objstore.Config {
	t.Helper()
	cfg := objstore.Config{
		Endpoint:        os.Getenv("TEST_S3_ENDPOINT"),
		Region:          os.Getenv("TEST_S3_REGION"),
		AccessKeyID:     os.Getenv("TEST_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("TEST_S3_SECRET_ACCESS_KEY"),
		Bucket:          objstore.InvoiceFilesBucket,
	}
	if cfg.Endpoint == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_S3_* is not set in CI")
		}
		t.Skip("TEST_S3_* not set; start the local stack with Storage (apps/api/README.md)")
	}
	if cfg.Region == "" {
		cfg.Region = "local"
	}
	return cfg
}

// Store returns a store for the local bucket.
func Store(t *testing.T) *objstore.Store {
	t.Helper()
	s, err := objstore.New(Config(t))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
