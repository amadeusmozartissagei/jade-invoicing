// Command admin runs operator tasks against the database as postgres, which
// the API itself never connects as. In phase 1 it creates tenants:
//
//	DATABASE_URL=... go run ./cmd/admin create-tenant -name "Beispiel GmbH" -jurisdiction DE -timezone Europe/Berlin
//
// The database URL comes from the environment only, never a flag, so the
// password stays out of shell history. The new API key is printed once, to
// stdout, and only its hash is stored. Vercel builds cmd/api only.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
	_ "time/tzdata" // validate time zones on any machine

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/admin"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/db"
)

const usage = `usage: admin create-tenant -name NAME -jurisdiction DE|JP -timezone ZONE [flags]

Creates a tenant, its empty seller profile, and its first API key, as postgres.
Reads the database URL from DATABASE_URL. Prints the key once, on stdout.
`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 || args[0] != "create-tenant" {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	in, err := parseCreateTenant(args[1:], stderr)
	if err != nil {
		return 2
	}
	if err := in.Validate(time.Now()); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}

	url := getenv("DATABASE_URL")
	if url == "" {
		_, _ = fmt.Fprintln(stderr, "DATABASE_URL is not set; it must connect as postgres")
		return 2
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer pool.Close()

	c, err := admin.CreateTenant(ctx, pool, in)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stderr, "tenant   %s\napi key  %s\n\nThe key below is shown only now; only its hash is stored.\n", c.TenantID, c.APIKeyID)
	if _, err := fmt.Fprintln(stdout, c.Key); err != nil {
		// The tenant exists but nobody has its key. Say which key to revoke.
		_, _ = fmt.Fprintf(stderr, "writing the key failed (%v); revoke api key %s and create a new one\n", err, c.APIKeyID)
		return 1
	}
	return 0
}

func parseCreateTenant(args []string, stderr io.Writer) (admin.TenantInput, error) {
	var in admin.TenantInput
	var expiresIn time.Duration
	fs := flag.NewFlagSet("create-tenant", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&in.Name, "name", "", "display name (required)")
	fs.StringVar(&in.Jurisdiction, "jurisdiction", "", "DE or JP (required)")
	fs.StringVar(&in.Timezone, "timezone", "", "IANA time zone, e.g. Europe/Berlin or Asia/Tokyo (required)")
	fs.StringVar(&in.JPTaxRounding, "jp-tax-rounding", "", "JP only: down, up, or half_up (required for JP)")
	fs.IntVar(&in.FiscalYearEndMonth, "fiscal-year-end-month", 12, "month the fiscal year ends, 1 to 12")
	fs.StringVar(&in.KeyName, "key-name", "initial", "label of the first API key")
	fs.DurationVar(&expiresIn, "key-expires-in", 0, "lifetime of the first key, e.g. 720h; 0 means no expiry")
	if err := fs.Parse(args); err != nil {
		return in, err
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected argument %q\n", fs.Arg(0))
		return in, fmt.Errorf("unexpected argument")
	}
	if expiresIn != 0 {
		t := time.Now().Add(expiresIn)
		in.KeyExpiresAt = &t
	}
	return in, nil
}
