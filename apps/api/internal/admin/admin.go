// Package admin holds operations that run as postgres, outside the API and
// its row-level security: in phase 1, creating a tenant with its first API
// key (docs/design/data-model.md, "tenants"). Self-service signup comes with
// the dashboard.
package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/apikey"
)

// TenantInput describes a new tenant and its first key.
type TenantInput struct {
	Name         string
	Jurisdiction string // DE or JP; the currency follows from it
	Timezone     string // IANA name; decides issue dates
	// JPTaxRounding is down, up, or half_up: required for JP, empty for DE.
	JPTaxRounding string
	// FiscalYearEndMonth is 1 to 12; 0 means 12 (calendar year).
	FiscalYearEndMonth int
	// KeyName labels the first API key; empty means "initial".
	KeyName string
	// KeyExpiresAt is when the first key stops working; nil means never.
	KeyExpiresAt *time.Time
}

// Created is the result. Key is the full API key: shown once, never stored.
type Created struct {
	TenantID uuid.UUID
	APIKeyID uuid.UUID
	Key      string
}

var currencies = map[string]string{"DE": "EUR", "JP": "JPY"}

// Validate checks in and fills its defaults. The database checks the same
// rules; checking first gives clear messages and runs no SQL for bad input.
func (in *TenantInput) Validate(now time.Time) error {
	var errs []error
	if in.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if _, ok := currencies[in.Jurisdiction]; !ok {
		errs = append(errs, fmt.Errorf("jurisdiction %q: want DE or JP", in.Jurisdiction))
	}
	// LoadLocation accepts "" and "Local" as the machine's zone, which is
	// not a tenant setting.
	if in.Timezone == "" || in.Timezone == "Local" {
		errs = append(errs, errors.New("timezone is required, as an IANA name such as Europe/Berlin"))
	} else if _, err := time.LoadLocation(in.Timezone); err != nil {
		errs = append(errs, fmt.Errorf("timezone %q is not an IANA time zone", in.Timezone))
	}
	switch {
	case in.Jurisdiction == "JP" && in.JPTaxRounding == "":
		errs = append(errs, errors.New("jp tax rounding is required for JP: down, up, or half_up"))
	case in.Jurisdiction == "JP" && in.JPTaxRounding != "down" && in.JPTaxRounding != "up" && in.JPTaxRounding != "half_up":
		errs = append(errs, fmt.Errorf("jp tax rounding %q: want down, up, or half_up", in.JPTaxRounding))
	case in.Jurisdiction != "JP" && in.JPTaxRounding != "":
		errs = append(errs, errors.New("jp tax rounding applies to JP tenants only"))
	}
	if in.FiscalYearEndMonth == 0 {
		in.FiscalYearEndMonth = 12
	}
	if in.FiscalYearEndMonth < 1 || in.FiscalYearEndMonth > 12 {
		errs = append(errs, fmt.Errorf("fiscal year end month %d: want 1 to 12", in.FiscalYearEndMonth))
	}
	if in.KeyName == "" {
		in.KeyName = "initial"
	}
	if in.KeyExpiresAt != nil && !in.KeyExpiresAt.After(now) {
		errs = append(errs, errors.New("key expiry must be in the future"))
	}
	return errors.Join(errs...)
}

// Beginner is a pool or connection that can start a transaction.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// CreateTenant creates the tenant, an empty seller profile, and the first API
// key in one transaction, with an audit entry for the tenant and the key.
// The default number series comes from a trigger on tenants. It must run as
// a role that bypasses RLS (postgres). Only the SHA-256 of the key's secret
// is stored.
func CreateTenant(ctx context.Context, db Beginner, in TenantInput) (Created, error) {
	if err := in.Validate(time.Now()); err != nil {
		return Created{}, err
	}
	var rounding *string
	if in.JPTaxRounding != "" {
		rounding = &in.JPTaxRounding
	}
	key, hash := apikey.Generate()

	var c Created
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			insert into app.tenants (name, jurisdiction, currency, timezone, jp_tax_rounding, fiscal_year_end_month)
			values ($1, $2, $3, $4, $5, $6)
			returning id`,
			in.Name, in.Jurisdiction, currencies[in.Jurisdiction], in.Timezone, rounding, in.FiscalYearEndMonth,
		).Scan(&c.TenantID)
		if err != nil {
			return fmt.Errorf("insert tenant: %w", err)
		}
		if _, err := tx.Exec(ctx, `insert into app.seller_profiles (tenant_id) values ($1)`, c.TenantID); err != nil {
			return fmt.Errorf("insert seller profile: %w", err)
		}
		err = tx.QueryRow(ctx, `
			insert into app.api_keys (tenant_id, name, prefix, secret_hash, expires_at)
			values ($1, $2, $3, $4, $5)
			returning id`,
			c.TenantID, in.KeyName, key.Prefix, hash[:], in.KeyExpiresAt,
		).Scan(&c.APIKeyID)
		if err != nil {
			return fmt.Errorf("insert api key: %w", err)
		}
		// No personal data in the audit log (data-model.md); the key's
		// prefix is public and identifies it.
		_, err = tx.Exec(ctx, `
			insert into app.audit_log (tenant_id, actor_type, action, subject_type, subject_id, details)
			values ($1, 'system', 'tenant.create', 'tenant', $1, '{}'),
			       ($1, 'system', 'api_key.create', 'api_key', $2,
			        jsonb_build_object('name', $3::text, 'prefix', $4::text))`,
			c.TenantID, c.APIKeyID, in.KeyName, key.Prefix,
		)
		if err != nil {
			return fmt.Errorf("insert audit log: %w", err)
		}
		return nil
	})
	if err != nil {
		return Created{}, err
	}
	c.Key = key.Full
	return c, nil
}
