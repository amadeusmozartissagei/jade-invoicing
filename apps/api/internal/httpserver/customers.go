package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/api"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/problem"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/store"
)

// externalRefConstraint is the unique (tenant_id, external_ref) constraint
// on app.customers, named by Postgres' default rule.
const externalRefConstraint = "customers_tenant_id_external_ref_key"

const (
	defaultPageSize = 25
	auditCustomer   = "customer"
)

func (h *handlers) CreateCustomer(ctx context.Context, req api.CreateCustomerRequestObject) (api.CreateCustomerResponseObject, error) {
	in := req.Body
	c, err := queries(ctx).CreateCustomer(ctx, store.CreateCustomerParams{
		ExternalRef:           value(in.ExternalRef),
		Kind:                  string(in.Kind),
		Name:                  in.Name,
		TradingName:           value(in.TradingName),
		AddressLine1:          value(in.AddressLine1),
		AddressLine2:          value(in.AddressLine2),
		AddressLine3:          value(in.AddressLine3),
		City:                  value(in.City),
		PostCode:              value(in.PostCode),
		CountrySubdivision:    value(in.CountrySubdivision),
		CountryCode:           value(in.CountryCode),
		VatID:                 value(in.VatId),
		LegalRegistrationID:   value(in.LegalRegistrationId),
		EstablishedInDe:       boolValue(in.EstablishedInDe),
		DefaultBuyerReference: value(in.DefaultBuyerReference),
		ContactName:           value(in.ContactName),
		ContactPhone:          value(in.ContactPhone),
		ContactEmail:          value(in.ContactEmail),
		Email:                 value(in.Email),
	})
	if isExternalRefConflict(err) {
		return api.CreateCustomer409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse: api.ConflictApplicationProblemPlusJSONResponse{
				Body: externalRefProblem(ctx),
			},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("create customer: %w", err)
	}
	if err := audit(ctx, "customer.create", auditCustomer, c.ID, nil); err != nil {
		return nil, err
	}
	location := "/v1/customers/" + c.ID.String()
	return api.CreateCustomer201JSONResponse{
		Body:    customerBody(c),
		Headers: api.CreateCustomer201ResponseHeaders{Location: &location},
	}, nil
}

func (h *handlers) GetCustomer(ctx context.Context, req api.GetCustomerRequestObject) (api.GetCustomerResponseObject, error) {
	c, err := queries(ctx).GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.GetCustomer404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: api.NotFoundApplicationProblemPlusJSONResponse{
				Body: customerNotFound(ctx),
			},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read customer: %w", err)
	}
	return api.GetCustomer200JSONResponse{Body: customerBody(c)}, nil
}

// PatchCustomer applies a JSON Merge Patch (RFC 7396): fields present are
// set, null clears, absent fields stay. The contract has already rejected
// unknown fields and null for kind and name. A patch that changes nothing
// writes nothing, so updated_at only moves on a real change.
func (h *handlers) PatchCustomer(ctx context.Context, req api.PatchCustomerRequestObject) (api.PatchCustomerResponseObject, error) {
	q := queries(ctx)
	c, err := q.GetCustomerForUpdate(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.PatchCustomer404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: api.NotFoundApplicationProblemPlusJSONResponse{
				Body: customerNotFound(ctx),
			},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock customer: %w", err)
	}

	changed := mergeCustomer(&c, req.Body)
	if len(changed) == 0 {
		return api.PatchCustomer200JSONResponse{Body: customerBody(c)}, nil
	}
	c, err = q.UpdateCustomer(ctx, store.UpdateCustomerParams{
		ID:                    c.ID,
		ExternalRef:           c.ExternalRef,
		Kind:                  c.Kind,
		Name:                  c.Name,
		TradingName:           c.TradingName,
		AddressLine1:          c.AddressLine1,
		AddressLine2:          c.AddressLine2,
		AddressLine3:          c.AddressLine3,
		City:                  c.City,
		PostCode:              c.PostCode,
		CountrySubdivision:    c.CountrySubdivision,
		CountryCode:           c.CountryCode,
		VatID:                 c.VatID,
		LegalRegistrationID:   c.LegalRegistrationID,
		EstablishedInDe:       c.EstablishedInDe,
		DefaultBuyerReference: c.DefaultBuyerReference,
		ContactName:           c.ContactName,
		ContactPhone:          c.ContactPhone,
		ContactEmail:          c.ContactEmail,
		Email:                 c.Email,
	})
	if isExternalRefConflict(err) {
		return api.PatchCustomer409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse: api.ConflictApplicationProblemPlusJSONResponse{
				Body: externalRefProblem(ctx),
			},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update customer: %w", err)
	}
	if err := audit(ctx, "customer.update", auditCustomer, c.ID, changed); err != nil {
		return nil, err
	}
	return api.PatchCustomer200JSONResponse{Body: customerBody(c)}, nil
}

// mergeCustomer applies patch to c and returns the names of the fields whose
// value changed, in the contract's spelling, for the audit log.
func mergeCustomer(c *store.AppCustomer, p *api.CustomerPatch) []string {
	var changed []string
	if p.Kind != nil && string(*p.Kind) != c.Kind {
		c.Kind = string(*p.Kind)
		changed = append(changed, "kind")
	}
	if p.Name != nil && *p.Name != c.Name {
		c.Name = *p.Name
		changed = append(changed, "name")
	}
	merge := func(name string, dst **string, n nullable.Nullable[string]) {
		mergeField(&changed, name, dst, n)
	}
	merge("external_ref", &c.ExternalRef, p.ExternalRef)
	merge("trading_name", &c.TradingName, p.TradingName)
	merge("address_line1", &c.AddressLine1, p.AddressLine1)
	merge("address_line2", &c.AddressLine2, p.AddressLine2)
	merge("address_line3", &c.AddressLine3, p.AddressLine3)
	merge("city", &c.City, p.City)
	merge("post_code", &c.PostCode, p.PostCode)
	merge("country_subdivision", &c.CountrySubdivision, p.CountrySubdivision)
	merge("country_code", &c.CountryCode, p.CountryCode)
	merge("vat_id", &c.VatID, p.VatId)
	merge("legal_registration_id", &c.LegalRegistrationID, p.LegalRegistrationId)
	if p.EstablishedInDe.IsSpecified() && mergeOne(&c.EstablishedInDe, boolValue(p.EstablishedInDe)) {
		changed = append(changed, "established_in_de")
	}
	merge("default_buyer_reference", &c.DefaultBuyerReference, p.DefaultBuyerReference)
	merge("contact_name", &c.ContactName, p.ContactName)
	merge("contact_phone", &c.ContactPhone, p.ContactPhone)
	mergeField(&changed, "contact_email", &c.ContactEmail, p.ContactEmail)
	mergeField(&changed, "email", &c.Email, p.Email)
	return changed
}

// mergeField applies one string field of a merge patch: absent stays, null
// clears, a value sets. A changed value adds name to changed.
func mergeField[T ~string](changed *[]string, name string, dst **string, n nullable.Nullable[T]) {
	if n.IsSpecified() && mergeOne(dst, value(n)) {
		*changed = append(*changed, name)
	}
}

// mergeOne sets *dst to v and reports whether the value changed.
func mergeOne[T comparable](dst **T, v *T) bool {
	old := *dst
	if (old == nil && v == nil) || (old != nil && v != nil && *old == *v) {
		return false
	}
	*dst = v
	return true
}

func (h *handlers) ListCustomers(ctx context.Context, req api.ListCustomersRequestObject) (api.ListCustomersResponseObject, error) {
	limit := defaultPageSize
	if req.Params.Limit != nil {
		limit = *req.Params.Limit // 1 to 100, checked by the contract
	}
	filter := customerFilter(req.Params.ExternalRef)
	params := store.ListCustomersParams{ExternalRef: req.Params.ExternalRef, RowLimit: int32(limit + 1)} //nolint:gosec // at most 101
	if req.Params.Cursor != nil {
		at, id, ok := decodeCursor(*req.Params.Cursor, filter)
		if !ok {
			return api.ListCustomers400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: api.BadRequestApplicationProblemPlusJSONResponse{
					Body: problem.New(problem.BadRequest, RequestID(ctx),
						"The cursor is malformed or was issued for other filters. Start again without a cursor."),
				},
			}, nil
		}
		params.AfterCreatedAt, params.AfterID = &at, &id
	}

	rows, err := queries(ctx).ListCustomers(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list customers: %w", err)
	}
	next := nullable.NewNullNullable[api.NextCursor]()
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = nullable.NewNullableWithValue(encodeCursor(last.CreatedAt, last.ID, filter))
	}
	data := make([]api.Customer, 0, len(rows))
	for _, c := range rows {
		data = append(data, customerBody(c))
	}
	return api.ListCustomers200JSONResponse{Body: api.CustomerList{Data: data, NextCursor: next}}, nil
}

// cursor is the position after which the next page starts: the last row's
// (created_at, id), and a hash of the filters it was issued for (api.md,
// "Pagination"). Clients treat it as opaque.
type cursor struct {
	T time.Time `json:"t"` // full microsecond precision, as stored
	I uuid.UUID `json:"i"`
	F string    `json:"f"`
}

func customerFilter(externalRef *string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("customers"))
	if externalRef != nil {
		_, _ = fmt.Fprintf(h, "\x00external_ref=%s", *externalRef)
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:8])
}

func encodeCursor(at time.Time, id uuid.UUID, filter string) string {
	b, _ := json.Marshal(cursor{T: at.UTC(), I: id, F: filter}) // cannot fail
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s, filter string) (time.Time, uuid.UUID, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	var c cursor
	if json.Unmarshal(b, &c) != nil || c.F != filter || c.T.IsZero() || c.I == uuid.Nil {
		return time.Time{}, uuid.Nil, false
	}
	return c.T, c.I, true
}

func customerBody(c store.AppCustomer) api.Customer {
	return api.Customer{
		Id:                    c.ID,
		ExternalRef:           orNull(c.ExternalRef),
		Kind:                  api.CustomerKind(c.Kind),
		Name:                  c.Name,
		TradingName:           orNull(c.TradingName),
		AddressLine1:          orNull(c.AddressLine1),
		AddressLine2:          orNull(c.AddressLine2),
		AddressLine3:          orNull(c.AddressLine3),
		City:                  orNull(c.City),
		PostCode:              orNull(c.PostCode),
		CountrySubdivision:    orNull(c.CountrySubdivision),
		CountryCode:           orNullAs[api.CountryCode](c.CountryCode),
		VatId:                 orNull(c.VatID),
		LegalRegistrationId:   orNull(c.LegalRegistrationID),
		EstablishedInDe:       boolOrNull(c.EstablishedInDe),
		DefaultBuyerReference: orNull(c.DefaultBuyerReference),
		ContactName:           orNull(c.ContactName),
		ContactPhone:          orNull(c.ContactPhone),
		ContactEmail:          orNullAs[openapi_types.Email](c.ContactEmail),
		Email:                 orNullAs[openapi_types.Email](c.Email),
		CreatedAt:             c.CreatedAt.UTC(),
		UpdatedAt:             c.UpdatedAt.UTC(),
	}
}

func boolValue(n nullable.Nullable[bool]) *bool {
	v, err := n.Get()
	if err != nil { // absent or null
		return nil
	}
	return &v
}

func boolOrNull(p *bool) nullable.Nullable[bool] {
	if p == nil {
		return nullable.NewNullNullable[bool]()
	}
	return nullable.NewNullableWithValue(*p)
}

func isExternalRefConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == externalRefConstraint
}

func externalRefProblem(ctx context.Context) api.Problem {
	return problem.New(problem.ConflictExternalRef, RequestID(ctx),
		"Another customer of this tenant has the same external_ref.")
}

func customerNotFound(ctx context.Context) api.Problem {
	return problem.New(problem.NotFound, RequestID(ctx), "No such customer.")
}

// audit records a change in the request's transaction. details holds only
// field names, never values, so the audit log stays free of personal data
// (data-model.md, "audit_log"; ADR 0014).
func audit(ctx context.Context, action, subjectType string, subject uuid.UUID, fields []string) error {
	details := []byte("{}")
	if fields != nil {
		var err error
		if details, err = json.Marshal(map[string][]string{"fields": fields}); err != nil {
			return err
		}
	}
	tr := tenantFrom(ctx)
	requestID := RequestID(ctx)
	if err := queries(ctx).InsertAuditLog(ctx, store.InsertAuditLogParams{
		ActorID:     &tr.keyID,
		Action:      action,
		SubjectType: &subjectType,
		SubjectID:   &subject,
		RequestID:   &requestID,
		Details:     string(details),
	}); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}
