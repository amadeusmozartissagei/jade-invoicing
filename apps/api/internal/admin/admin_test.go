package admin_test

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/admin"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/apikey"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/dbtest"
)

func TestValidate(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	de := func() admin.TenantInput {
		return admin.TenantInput{Name: "Beispiel GmbH", Jurisdiction: "DE", Timezone: "Europe/Berlin"}
	}
	cases := map[string]func(*admin.TenantInput){
		"no name":             func(in *admin.TenantInput) { in.Name = "" },
		"bad jurisdiction":    func(in *admin.TenantInput) { in.Jurisdiction = "FR" },
		"no timezone":         func(in *admin.TenantInput) { in.Timezone = "" },
		"local timezone":      func(in *admin.TenantInput) { in.Timezone = "Local" },
		"unknown timezone":    func(in *admin.TenantInput) { in.Timezone = "Mars/Olympus" },
		"rounding for DE":     func(in *admin.TenantInput) { in.JPTaxRounding = "down" },
		"JP without rounding": func(in *admin.TenantInput) { in.Jurisdiction, in.Timezone = "JP", "Asia/Tokyo" },
		"JP bad rounding": func(in *admin.TenantInput) {
			in.Jurisdiction, in.Timezone, in.JPTaxRounding = "JP", "Asia/Tokyo", "nearest"
		},
		"month 13":       func(in *admin.TenantInput) { in.FiscalYearEndMonth = 13 },
		"expired key":    func(in *admin.TenantInput) { in.KeyExpiresAt = &past },
		"negative month": func(in *admin.TenantInput) { in.FiscalYearEndMonth = -1 },
	}
	for name, mutate := range cases {
		in := de()
		mutate(&in)
		if err := in.Validate(now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	in := de()
	if err := in.Validate(now); err != nil {
		t.Fatal(err)
	}
	if in.FiscalYearEndMonth != 12 || in.KeyName != "initial" {
		t.Errorf("defaults not applied: %+v", in)
	}
}

func TestCreateTenant(t *testing.T) {
	pool := dbtest.AdminPool(t)
	ctx := t.Context()
	expires := time.Now().Add(24 * time.Hour).Truncate(time.Microsecond)

	c, err := admin.CreateTenant(ctx, pool, admin.TenantInput{
		Name: "Test " + t.Name(), Jurisdiction: "JP", Timezone: "Asia/Tokyo", JPTaxRounding: "half_up",
		FiscalYearEndMonth: 3, KeyName: "ci", KeyExpiresAt: &expires,
	})
	if err != nil {
		t.Fatal(err)
	}

	var currency, rounding, status string
	var month int
	err = pool.QueryRow(ctx, `select currency, jp_tax_rounding, fiscal_year_end_month, status from app.tenants where id = $1`,
		c.TenantID).Scan(&currency, &rounding, &month, &status)
	if err != nil {
		t.Fatal(err)
	}
	if currency != "JPY" || rounding != "half_up" || month != 3 || status != "active" {
		t.Errorf("tenant = %s %s %d %s", currency, rounding, month, status)
	}

	var profiles, series, audits int
	err = pool.QueryRow(ctx, `select
		(select count(*) from app.seller_profiles where tenant_id = $1 and legal_name is null),
		(select count(*) from app.number_series where tenant_id = $1 and code = 'default'),
		(select count(*) from app.audit_log where tenant_id = $1 and actor_type = 'system'
		   and action in ('tenant.create', 'api_key.create'))`, c.TenantID).Scan(&profiles, &series, &audits)
	if err != nil {
		t.Fatal(err)
	}
	if profiles != 1 || series != 1 || audits != 2 {
		t.Errorf("profiles %d, series %d, audit rows %d; want 1, 1, 2", profiles, series, audits)
	}

	// The printed key works with the stored hash; the secret itself is stored
	// nowhere.
	prefix, secret, ok := apikey.Parse(c.Key)
	if !ok {
		t.Fatalf("key %q does not parse", c.Key)
	}
	var hash []byte
	var name string
	var gotExpires time.Time
	err = pool.QueryRow(ctx, `select secret_hash, name, expires_at from app.api_keys where id = $1 and prefix = $2 and tenant_id = $3`,
		c.APIKeyID, prefix, c.TenantID).Scan(&hash, &name, &gotExpires)
	if err != nil {
		t.Fatal(err)
	}
	if want := sha256.Sum256([]byte(secret)); string(hash) != string(want[:]) {
		t.Error("stored hash is not SHA-256 of the secret")
	}
	if name != "ci" || !gotExpires.Equal(expires) {
		t.Errorf("key name %q, expires %v; want ci, %v", name, gotExpires, expires)
	}
	var leaks int
	err = pool.QueryRow(ctx, `select count(*) from app.audit_log where tenant_id = $1 and details::text like '%' || $2 || '%'`,
		c.TenantID, secret).Scan(&leaks)
	if err != nil || leaks != 0 {
		t.Errorf("secret found in audit log (%d rows, err %v)", leaks, err)
	}
}

func TestInvalidInputWritesNothing(t *testing.T) {
	pool := dbtest.AdminPool(t)
	name := "Test " + t.Name()
	_, err := admin.CreateTenant(t.Context(), pool, admin.TenantInput{Name: name, Jurisdiction: "JP", Timezone: "Asia/Tokyo"})
	if err == nil || !strings.Contains(err.Error(), "rounding") {
		t.Fatalf("err = %v, want a rounding error", err)
	}
	var n int
	if err := pool.QueryRow(t.Context(), `select count(*) from app.tenants where name = $1`, name).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d tenants named %q (err %v)", n, name, err)
	}
}
