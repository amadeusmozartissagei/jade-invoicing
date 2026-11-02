package httpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/api"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/store"
)

// GetTenant returns the tenant authenticate already read; no second query.
func (h *handlers) GetTenant(ctx context.Context, _ api.GetTenantRequestObject) (api.GetTenantResponseObject, error) {
	t := tenantFrom(ctx).tenant
	body := api.Tenant{
		Id:                 t.ID,
		Name:               t.Name,
		Jurisdiction:       api.Jurisdiction(t.Jurisdiction),
		Currency:           api.Currency(t.Currency),
		Timezone:           t.Timezone,
		JpTaxRounding:      nullable.NewNullNullable[api.TenantJpTaxRounding](),
		FiscalYearEndMonth: int(t.FiscalYearEndMonth),
		Status:             api.TenantStatus(t.Status),
		CreatedAt:          t.CreatedAt.UTC(), // RFC 3339 in UTC (api.md)
	}
	if t.JpTaxRounding != nil {
		body.JpTaxRounding = nullable.NewNullableWithValue(api.TenantJpTaxRounding(*t.JpTaxRounding))
	}
	return api.GetTenant200JSONResponse{Body: body}, nil
}

func (h *handlers) GetSellerProfile(ctx context.Context, _ api.GetSellerProfileRequestObject) (api.GetSellerProfileResponseObject, error) {
	p, err := queries(ctx).GetSellerProfile(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		// The admin script creates the profile with the tenant, and the
		// contract has no 404 here: a missing row is a bug.
		return nil, errors.New("tenant has no seller profile")
	}
	if err != nil {
		return nil, fmt.Errorf("read seller profile: %w", err)
	}
	return api.GetSellerProfile200JSONResponse{Body: sellerProfileBody(p)}, nil
}

// PutSellerProfile replaces the seller details; omitted fields become null.
// Completeness is checked when an invoice is issued, not here.
func (h *handlers) PutSellerProfile(ctx context.Context, req api.PutSellerProfileRequestObject) (api.PutSellerProfileResponseObject, error) {
	in := req.Body
	p, err := queries(ctx).PutSellerProfile(ctx, store.PutSellerProfileParams{
		LegalName:            value(in.LegalName),
		TradingName:          value(in.TradingName),
		AddressLine1:         value(in.AddressLine1),
		AddressLine2:         value(in.AddressLine2),
		AddressLine3:         value(in.AddressLine3),
		City:                 value(in.City),
		PostCode:             value(in.PostCode),
		CountrySubdivision:   value(in.CountrySubdivision),
		CountryCode:          value(in.CountryCode),
		VatID:                value(in.VatId),
		TaxNumber:            value(in.TaxNumber),
		JpRegistrationNumber: value(in.JpRegistrationNumber),
		ContactName:          value(in.ContactName),
		ContactPhone:         value(in.ContactPhone),
		ContactEmail:         value(in.ContactEmail),
		Iban:                 value(in.Iban),
		Bic:                  value(in.Bic),
		AccountName:          value(in.AccountName),
	})
	if err != nil {
		// The contract's patterns match the table's checks, so a check
		// violation here means the two drifted apart: a bug, logged as one.
		return nil, fmt.Errorf("write seller profile: %w", err)
	}
	return api.PutSellerProfile200JSONResponse{Body: sellerProfileBody(p)}, nil
}

func sellerProfileBody(p store.AppSellerProfile) api.SellerProfile {
	return api.SellerProfile{
		LegalName:            orNull(p.LegalName),
		TradingName:          orNull(p.TradingName),
		AddressLine1:         orNull(p.AddressLine1),
		AddressLine2:         orNull(p.AddressLine2),
		AddressLine3:         orNull(p.AddressLine3),
		City:                 orNull(p.City),
		PostCode:             orNull(p.PostCode),
		CountrySubdivision:   orNull(p.CountrySubdivision),
		CountryCode:          orNull(p.CountryCode),
		VatId:                orNull(p.VatID),
		TaxNumber:            orNull(p.TaxNumber),
		JpRegistrationNumber: orNull(p.JpRegistrationNumber),
		ContactName:          orNull(p.ContactName),
		ContactPhone:         orNull(p.ContactPhone),
		ContactEmail:         orNullAs[openapi_types.Email](p.ContactEmail),
		Iban:                 orNull(p.Iban),
		Bic:                  orNull(p.Bic),
		AccountName:          orNull(p.AccountName),
		UpdatedAt:            p.UpdatedAt.UTC(),
	}
}

// value turns an absent or null request field into SQL null.
func value[T ~string](n nullable.Nullable[T]) *string {
	v, err := n.Get()
	if err != nil { // absent or null
		return nil
	}
	s := string(v)
	return &s
}

// orNull writes every field of a response, null when unset, so a client sees
// the full shape.
func orNull(p *string) nullable.Nullable[string] { return orNullAs[string](p) }

func orNullAs[T ~string](p *string) nullable.Nullable[T] {
	if p == nil {
		return nullable.NewNullNullable[T]()
	}
	return nullable.NewNullableWithValue(T(*p))
}
