package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/api"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/invoicecalc"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/problem"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/store"
)

const auditInvoice = "invoice"

// Field error codes from docs/api/errors.md that only the handlers produce;
// the contract cannot express these rules.
const (
	codeMustBePositive     = "must_be_positive"
	codeTooManyDecimals    = "too_many_decimals"
	codeRateNotValid       = "rate_not_valid"
	codeInvalidPeriod      = "invalid_period"
	codeMustMatchPreceding = "must_match_preceding"
	codeRequired           = "required"
	codeNotAllowed         = "not_allowed"
	codeInvalidValue       = "invalid_value"
	codeOutOfRange         = "out_of_range"
)

// draft is a draft's editable content in Go types: what a create sets and a
// merge patch changes. The tax engine computes everything else from it.
type draft struct {
	CustomerID        uuid.UUID
	SeriesID          uuid.UUID
	SupplyDate        *time.Time
	SupplyPeriodStart *time.Time
	SupplyPeriodEnd   *time.Time
	DueDate           *time.Time
	BuyerReference    *string
	OrderReference    *string
	Note              *string
	PaymentTerms      *string
	Prepaid           decimal.Decimal
	Lines             []draftLine
}

type draftLine struct {
	Name              string
	Description       *string
	Quantity          decimal.Decimal
	UnitCode          string
	UnitPrice         decimal.Decimal
	PriceBaseQuantity decimal.Decimal
	TaxCategory       string
	TaxRate           decimal.Decimal
	PeriodStart       *time.Time
	PeriodEnd         *time.Time
}

func (l draftLine) equal(o draftLine) bool {
	return l.Name == o.Name && eqPtr(l.Description, o.Description) && l.Quantity.Equal(o.Quantity) &&
		l.UnitCode == o.UnitCode && l.UnitPrice.Equal(o.UnitPrice) && l.PriceBaseQuantity.Equal(o.PriceBaseQuantity) &&
		l.TaxCategory == o.TaxCategory && l.TaxRate.Equal(o.TaxRate) &&
		eqDate(l.PeriodStart, o.PeriodStart) && eqDate(l.PeriodEnd, o.PeriodEnd)
}

// fieldErrors collects field problems: a 422 validation_failed for a
// request, or a 409 invoice_incomplete for a draft that cannot be issued.
type fieldErrors []api.FieldError

func (f *fieldErrors) add(pointer, code, detail string) {
	*f = append(*f, api.FieldError{Pointer: pointer, Code: code, Detail: detail})
}

func (f fieldErrors) problem(ctx context.Context, kind problem.Kind) api.Problem {
	sort.SliceStable(f, func(i, j int) bool { return f[i].Pointer < f[j].Pointer })
	p := problem.New(kind, RequestID(ctx), fieldCount(len(f)))
	errs := []api.FieldError(f)
	p.Errors = &errs
	return p
}

func (h *handlers) CreateInvoice(ctx context.Context, req api.CreateInvoiceRequestObject) (api.CreateInvoiceResponseObject, error) {
	in := req.Body
	tr := tenantFrom(ctx)
	q := queries(ctx)
	var errs fieldErrors

	docType := api.DocumentTypeInvoice
	if in.DocumentType != nil {
		docType = *in.DocumentType
	}
	var preceding *store.AppInvoice
	switch {
	case docType == api.DocumentTypeInvoice && in.PrecedingInvoiceId != nil:
		errs.add("/preceding_invoice_id", codeNotAllowed, "A plain invoice refers to no preceding invoice.")
	case docType != api.DocumentTypeInvoice && in.PrecedingInvoiceId == nil:
		errs.add("/preceding_invoice_id", codeRequired, "A "+string(docType)+" must refer to the invoice it credits or corrects.")
	case in.PrecedingInvoiceId != nil:
		p, err := q.GetInvoice(ctx, *in.PrecedingInvoiceId)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			errs.add("/preceding_invoice_id", codeInvalidValue, "No such invoice.")
		case err != nil:
			return nil, fmt.Errorf("read preceding invoice: %w", err)
		case !creditable(p):
			return api.CreateInvoice409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: api.ConflictApplicationProblemPlusJSONResponse{
					Body: problem.New(problem.InvoiceNotIssued, RequestID(ctx), fmt.Sprintf(
						"The preceding document is a %s %s; only an issued invoice or corrected invoice can be credited or corrected.",
						p.Status, p.DocumentType)),
				},
			}, nil
		default:
			preceding = &p
		}
	}

	d := draft{
		SupplyDate:        dateValue(in.SupplyDate),
		SupplyPeriodStart: dateValue(in.SupplyPeriodStart),
		SupplyPeriodEnd:   dateValue(in.SupplyPeriodEnd),
		DueDate:           dateValue(in.DueDate),
		BuyerReference:    value(in.BuyerReference),
		OrderReference:    value(in.OrderReference),
		Note:              value(in.Note),
		PaymentTerms:      value(in.PaymentTerms),
		Prepaid:           decimal.Zero,
	}
	if in.PrepaidAmount != nil {
		d.Prepaid = decimal.RequireFromString(*in.PrepaidAmount) // pattern-checked by the contract
	}
	if in.Lines != nil {
		d.Lines = linesFromInput(*in.Lines)
	}

	// The customer: given, or the preceding invoice's, which it must equal.
	switch {
	case preceding != nil && in.CustomerId != nil && *in.CustomerId != preceding.CustomerID:
		errs.add("/customer_id", codeMustMatchPreceding, "A credit note or corrected invoice is for the preceding invoice's customer.")
	case preceding != nil:
		d.CustomerID = preceding.CustomerID
	case in.CustomerId != nil:
		d.CustomerID = *in.CustomerId
	case docType == api.DocumentTypeInvoice:
		errs.add("/customer_id", codeRequired, "An invoice needs a customer.")
	}
	if d.CustomerID != uuid.Nil {
		c, err := q.GetCustomer(ctx, d.CustomerID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			errs.add("/customer_id", codeInvalidValue, "No such customer.")
		case err != nil:
			return nil, fmt.Errorf("read customer: %w", err)
		case !in.BuyerReference.IsSpecified():
			// BT-10 defaults to the customer's reference. In DE it then falls
			// back to "-", which is enough for VAT purposes [BMF-FAQ Q6];
			// Japan has no such field, so a JP draft keeps none.
			switch {
			case c.DefaultBuyerReference != nil:
				d.BuyerReference = c.DefaultBuyerReference
			case tr.tenant.Jurisdiction == "DE":
				dash := "-"
				d.BuyerReference = &dash
			}
		}
	}

	var err error
	if d.SeriesID, err = h.seriesFor(ctx, in.SeriesId, &errs); err != nil {
		return nil, err
	}

	res, err := checkDraft(ctx, tr.tenant, taxRounding(tr.tenant), &d, true, &errs)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return api.CreateInvoice422ApplicationProblemPlusJSONResponse{
			UnprocessableContentApplicationProblemPlusJSONResponse: api.UnprocessableContentApplicationProblemPlusJSONResponse{
				Body: errs.problem(ctx, problem.ValidationFailed),
			},
		}, nil
	}

	var precedingID *uuid.UUID
	if preceding != nil {
		precedingID = &preceding.ID
	}
	inv, err := q.CreateInvoice(ctx, store.CreateInvoiceParams{
		DocumentType:       string(docType),
		CustomerID:         d.CustomerID,
		SeriesID:           d.SeriesID,
		PrecedingInvoiceID: precedingID,
		Jurisdiction:       tr.tenant.Jurisdiction,
		Currency:           tr.tenant.Currency,
		SupplyDate:         d.SupplyDate,
		SupplyPeriodStart:  d.SupplyPeriodStart,
		SupplyPeriodEnd:    d.SupplyPeriodEnd,
		DueDate:            d.DueDate,
		BuyerReference:     d.BuyerReference,
		OrderReference:     d.OrderReference,
		Note:               d.Note,
		PaymentTerms:       d.PaymentTerms,
		LineNetTotal:       res.LineNetTotal,
		TaxExclusiveTotal:  res.TaxExclusiveTotal,
		TaxTotal:           res.TaxTotal,
		TaxInclusiveTotal:  res.TaxInclusiveTotal,
		PrepaidAmount:      d.Prepaid,
		AmountDue:          res.AmountDue,
	})
	if err != nil {
		return nil, fmt.Errorf("create invoice: %w", err)
	}
	if err := writeLines(ctx, inv.ID, d.Lines, res); err != nil {
		return nil, err
	}
	if err := writeBreakdowns(ctx, inv.ID, res); err != nil {
		return nil, err
	}
	if err := audit(ctx, "invoice.create", auditInvoice, inv.ID, nil); err != nil {
		return nil, err
	}

	body, err := loadInvoice(ctx, inv)
	if err != nil {
		return nil, err
	}
	location, etag := "/v1/invoices/"+inv.ID.String(), etagOf(inv.Version)
	return api.CreateInvoice201JSONResponse{
		Body:    body,
		Headers: api.CreateInvoice201ResponseHeaders{Location: &location, ETag: &etag},
	}, nil
}

func (h *handlers) GetInvoice(ctx context.Context, req api.GetInvoiceRequestObject) (api.GetInvoiceResponseObject, error) {
	inv, err := queries(ctx).GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.GetInvoice404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: api.NotFoundApplicationProblemPlusJSONResponse{Body: invoiceNotFound(ctx)},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read invoice: %w", err)
	}
	body, err := loadInvoice(ctx, inv)
	if err != nil {
		return nil, err
	}
	etag := etagOf(inv.Version)
	return api.GetInvoice200JSONResponse{Body: body, Headers: api.GetInvoice200ResponseHeaders{ETag: &etag}}, nil
}

// PatchInvoice applies a JSON Merge Patch (RFC 7396) to a draft. lines, when
// present, replaces all lines. The totals and the tax breakdown are computed
// again on every change. A patch that changes nothing writes nothing and
// keeps the version.
func (h *handlers) PatchInvoice(ctx context.Context, req api.PatchInvoiceRequestObject) (api.PatchInvoiceResponseObject, error) {
	q := queries(ctx)
	tr := tenantFrom(ctx)
	inv, err := q.GetInvoiceForUpdate(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.PatchInvoice404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: api.NotFoundApplicationProblemPlusJSONResponse{Body: invoiceNotFound(ctx)},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock invoice: %w", err)
	}
	if !ifMatch(req.Params.IfMatch, inv.Version) {
		return api.PatchInvoice412ApplicationProblemPlusJSONResponse{
			PreconditionFailedApplicationProblemPlusJSONResponse: api.PreconditionFailedApplicationProblemPlusJSONResponse{
				Body: preconditionFailed(ctx, inv.Version),
			},
		}, nil
	}
	if inv.Status != "draft" {
		return api.PatchInvoice409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse: api.ConflictApplicationProblemPlusJSONResponse{Body: notDraft(ctx, inv)},
		}, nil
	}

	lines, err := q.ListInvoiceLines(ctx, []string{inv.ID.String()})
	if err != nil {
		return nil, fmt.Errorf("read lines: %w", err)
	}
	d := draftFromRow(inv, lines)
	p := req.Body
	var errs fieldErrors
	changed := mergeDraft(&d, p)
	if len(changed) == 0 {
		// Nothing to write, so nothing to check: the stored draft was valid
		// when it was saved.
		body, err := loadInvoice(ctx, inv)
		if err != nil {
			return nil, err
		}
		etag := etagOf(inv.Version)
		return api.PatchInvoice200JSONResponse{Body: body, Headers: api.PatchInvoice200ResponseHeaders{ETag: &etag}}, nil
	}

	if slices.Contains(changed, "customer_id") {
		if inv.PrecedingInvoiceID != nil {
			errs.add("/customer_id", codeMustMatchPreceding, "A credit note or corrected invoice is for the preceding invoice's customer.")
		} else if _, err := q.GetCustomer(ctx, d.CustomerID); errors.Is(err, pgx.ErrNoRows) {
			errs.add("/customer_id", codeInvalidValue, "No such customer.")
		} else if err != nil {
			return nil, fmt.Errorf("read customer: %w", err)
		}
	}
	if slices.Contains(changed, "series_id") {
		if _, err := h.seriesFor(ctx, p.SeriesId, &errs); err != nil {
			return nil, err
		}
	}

	res, err := checkDraft(ctx, tr.tenant, taxRounding(tr.tenant), &d, p.Lines != nil, &errs)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return api.PatchInvoice422ApplicationProblemPlusJSONResponse{
			UnprocessableContentApplicationProblemPlusJSONResponse: api.UnprocessableContentApplicationProblemPlusJSONResponse{
				Body: errs.problem(ctx, problem.ValidationFailed),
			},
		}, nil
	}

	{
		inv, err = q.UpdateDraft(ctx, store.UpdateDraftParams{
			ID:                inv.ID,
			CustomerID:        d.CustomerID,
			SeriesID:          d.SeriesID,
			SupplyDate:        d.SupplyDate,
			SupplyPeriodStart: d.SupplyPeriodStart,
			SupplyPeriodEnd:   d.SupplyPeriodEnd,
			DueDate:           d.DueDate,
			BuyerReference:    d.BuyerReference,
			OrderReference:    d.OrderReference,
			Note:              d.Note,
			PaymentTerms:      d.PaymentTerms,
			LineNetTotal:      res.LineNetTotal,
			TaxExclusiveTotal: res.TaxExclusiveTotal,
			TaxTotal:          res.TaxTotal,
			TaxInclusiveTotal: res.TaxInclusiveTotal,
			PrepaidAmount:     d.Prepaid,
			AmountDue:         res.AmountDue,
		})
		if err != nil {
			return nil, fmt.Errorf("update draft: %w", err)
		}
		if slices.Contains(changed, "lines") {
			if err := q.DeleteInvoiceLines(ctx, inv.ID); err != nil {
				return nil, fmt.Errorf("delete lines: %w", err)
			}
			if err := writeLines(ctx, inv.ID, d.Lines, res); err != nil {
				return nil, err
			}
		}
		// The breakdown is rewritten on every change: the tenant's Japanese
		// rounding mode may have changed since the draft was last saved.
		if err := q.DeleteTaxBreakdowns(ctx, inv.ID); err != nil {
			return nil, fmt.Errorf("delete breakdowns: %w", err)
		}
		if err := writeBreakdowns(ctx, inv.ID, res); err != nil {
			return nil, err
		}
		if err := audit(ctx, "invoice.update", auditInvoice, inv.ID, changed); err != nil {
			return nil, err
		}
	}

	body, err := loadInvoice(ctx, inv)
	if err != nil {
		return nil, err
	}
	etag := etagOf(inv.Version)
	return api.PatchInvoice200JSONResponse{Body: body, Headers: api.PatchInvoice200ResponseHeaders{ETag: &etag}}, nil
}

func (h *handlers) DeleteInvoice(ctx context.Context, req api.DeleteInvoiceRequestObject) (api.DeleteInvoiceResponseObject, error) {
	q := queries(ctx)
	inv, err := q.GetInvoiceForUpdate(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.DeleteInvoice404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: api.NotFoundApplicationProblemPlusJSONResponse{Body: invoiceNotFound(ctx)},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock invoice: %w", err)
	}
	if !ifMatch(req.Params.IfMatch, inv.Version) {
		return api.DeleteInvoice412ApplicationProblemPlusJSONResponse{
			PreconditionFailedApplicationProblemPlusJSONResponse: api.PreconditionFailedApplicationProblemPlusJSONResponse{
				Body: preconditionFailed(ctx, inv.Version),
			},
		}, nil
	}
	if inv.Status != "draft" {
		return api.DeleteInvoice409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse: api.ConflictApplicationProblemPlusJSONResponse{Body: notDraft(ctx, inv)},
		}, nil
	}
	if n, err := q.DeleteDraft(ctx, inv.ID); err != nil || n != 1 {
		return nil, fmt.Errorf("delete draft: %d rows, %w", n, err)
	}
	if err := audit(ctx, "invoice.delete", auditInvoice, inv.ID, nil); err != nil {
		return nil, err
	}
	return api.DeleteInvoice204Response{}, nil
}

func (h *handlers) ListInvoices(ctx context.Context, req api.ListInvoicesRequestObject) (api.ListInvoicesResponseObject, error) {
	pr := req.Params
	limit := defaultPageSize
	if pr.Limit != nil {
		limit = *pr.Limit // 1 to 100, checked by the contract
	}
	params := store.ListInvoicesParams{
		Status:        (*string)(pr.Status),
		DocumentType:  (*string)(pr.DocumentType),
		CustomerID:    pr.CustomerId,
		CreatedAfter:  pr.CreatedAfter,
		CreatedBefore: pr.CreatedBefore,
		RowLimit:      int32(limit + 1), //nolint:gosec // at most 101
	}
	filter := invoiceFilter(pr)
	if pr.Cursor != nil {
		at, id, ok := decodeCursor(*pr.Cursor, filter)
		if !ok {
			return api.ListInvoices400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: api.BadRequestApplicationProblemPlusJSONResponse{
					Body: problem.New(problem.BadRequest, RequestID(ctx),
						"The cursor is malformed or was issued for other filters. Start again without a cursor."),
				},
			}, nil
		}
		params.AfterCreatedAt, params.AfterID = &at, &id
	}

	rows, err := queries(ctx).ListInvoices(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list invoices: %w", err)
	}
	next := nullable.NewNullNullable[api.NextCursor]()
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = nullable.NewNullableWithValue(encodeCursor(last.CreatedAt, last.ID, filter))
	}
	data, err := loadInvoices(ctx, rows)
	if err != nil {
		return nil, err
	}
	return api.ListInvoices200JSONResponse{Body: api.InvoiceList{Data: data, NextCursor: next}}, nil
}

// creditable reports whether a credit note or corrected invoice may refer to
// inv (data-model.md, "Invoice lifecycle").
func creditable(inv store.AppInvoice) bool {
	return inv.Status == "issued" && (inv.DocumentType == "invoice" || inv.DocumentType == "corrected_invoice")
}

// seriesFor returns the series a draft is numbered from: the one given, which
// must be the tenant's, or the tenant's default.
func (h *handlers) seriesFor(ctx context.Context, given *uuid.UUID, errs *fieldErrors) (uuid.UUID, error) {
	q := queries(ctx)
	if given == nil {
		id, err := q.GetDefaultSeriesID(ctx)
		if err != nil {
			return uuid.Nil, fmt.Errorf("read default series: %w", err)
		}
		return id, nil
	}
	id, err := q.GetSeriesID(ctx, *given)
	if errors.Is(err, pgx.ErrNoRows) {
		errs.add("/series_id", codeInvalidValue, "No such number series.")
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("read series: %w", err)
	}
	return id, nil
}

// checkDraft checks the domain rules the contract cannot express, adding
// field errors to errs, and computes the totals when there are none.
//
// A line's rate must be valid for the jurisdiction on the supply date. A
// draft may not have one yet; then the rate must be valid today in the
// tenant's time zone. Issue checks again against the final date. When the
// lines are not in the request body (a PATCH of other fields), a rate that
// the new date makes invalid is reported at the date field instead, since
// pointers point into the request body.
func checkDraft(ctx context.Context, t store.AppTenant, rounding invoicecalc.Rounding, d *draft, linesInBody bool, errs *fieldErrors) (invoicecalc.Result, error) {
	minor, ok := invoicecalc.MinorUnits(t.Currency)
	if !ok {
		return invoicecalc.Result{}, fmt.Errorf("tenant currency %q", t.Currency)
	}
	if !invoicecalc.FitsCurrency(d.Prepaid, minor) {
		errs.add("/prepaid_amount", codeTooManyDecimals, fmt.Sprintf("%s amounts have at most %d decimals.", t.Currency, minor))
	}
	if d.SupplyPeriodStart != nil && d.SupplyPeriodEnd != nil && d.SupplyPeriodEnd.Before(*d.SupplyPeriodStart) {
		errs.add("/supply_period_end", codeInvalidPeriod, "The supply period ends before it starts.")
	}

	onDate, datePtr, err := rateDate(t, d)
	if err != nil {
		return invoicecalc.Result{}, err
	}
	valid := map[string]bool{}
	for i, l := range d.Lines {
		ptr := "/lines/" + strconv.Itoa(i)
		if !l.Quantity.IsPositive() {
			errs.add(ptr+"/quantity", codeMustBePositive, "quantity must be greater than 0")
		}
		if !l.PriceBaseQuantity.IsPositive() {
			errs.add(ptr+"/price_base_quantity", codeMustBePositive, "price_base_quantity must be greater than 0")
		}
		if l.PeriodStart != nil && l.PeriodEnd != nil && l.PeriodEnd.Before(*l.PeriodStart) {
			errs.add(ptr+"/period_end", codeInvalidPeriod, "The line period ends before it starts.")
		}
		key := l.TaxCategory + "/" + l.TaxRate.String()
		ok, seen := valid[key]
		if !seen {
			ok, err = queries(ctx).RateIsValid(ctx, store.RateIsValidParams{
				Jurisdiction: t.Jurisdiction,
				TaxCategory:  l.TaxCategory,
				Rate:         l.TaxRate,
				OnDate:       onDate,
			})
			if err != nil {
				return invoicecalc.Result{}, fmt.Errorf("check rate: %w", err)
			}
			valid[key] = ok
		}
		if !ok {
			detail := fmt.Sprintf("%s%% is not a %s rate on %s.", l.TaxRate, t.Jurisdiction, onDate.Format(time.DateOnly))
			if linesInBody {
				errs.add(ptr+"/tax_rate", codeRateNotValid, detail)
			} else {
				errs.add(datePtr, codeRateNotValid, fmt.Sprintf("Line %d: %s", i+1, detail))
			}
		}
	}
	if len(*errs) > 0 {
		return invoicecalc.Result{}, nil
	}

	in := make([]invoicecalc.Line, len(d.Lines))
	for i, l := range d.Lines {
		in[i] = invoicecalc.Line{
			Quantity: l.Quantity, UnitPrice: l.UnitPrice, PriceBaseQuantity: l.PriceBaseQuantity,
			TaxCategory: l.TaxCategory, TaxRate: l.TaxRate,
		}
	}
	res, err := invoicecalc.Calculate(in, invoicecalc.Params{MinorUnits: minor, Rounding: rounding, Prepaid: d.Prepaid})
	var tooLarge *invoicecalc.TooLargeError
	if errors.As(err, &tooLarge) {
		ptr := "/lines"
		if tooLarge.Line >= 0 {
			ptr += "/" + strconv.Itoa(tooLarge.Line)
		}
		errs.add(ptr, codeOutOfRange, "An amount would exceed 16 digits before the decimal point.")
		return invoicecalc.Result{}, nil
	}
	if err != nil {
		return invoicecalc.Result{}, err
	}
	return res, nil
}

// rateDate is the date a draft's rates must be valid on, and the pointer of
// the field it came from: the supply date, else the end of the supply period,
// else its start, else today in the tenant's time zone (pointer "").
func rateDate(t store.AppTenant, d *draft) (time.Time, string, error) {
	switch {
	case d.SupplyDate != nil:
		return *d.SupplyDate, "/supply_date", nil
	case d.SupplyPeriodEnd != nil:
		return *d.SupplyPeriodEnd, "/supply_period_end", nil
	case d.SupplyPeriodStart != nil:
		return *d.SupplyPeriodStart, "/supply_period_start", nil
	}
	today, err := tenantToday(t)
	return today, "", err
}

// tenantToday is today's date in the tenant's time zone, which decides
// business dates such as the issue date (data-model.md, "Conventions").
func tenantToday(t store.AppTenant) (time.Time, error) {
	loc, err := time.LoadLocation(t.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("tenant time zone: %w", err)
	}
	y, m, day := time.Now().In(loc).Date()
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC), nil
}

// taxRounding is the jurisdiction's tax rounding: commercial for DE, the
// tenant's choice for JP (data-model.md, "Tax calculation").
func taxRounding(t store.AppTenant) invoicecalc.Rounding {
	if t.Jurisdiction == "JP" && t.JpTaxRounding != nil {
		return invoicecalc.Rounding(*t.JpTaxRounding)
	}
	return invoicecalc.HalfAwayFromZero
}

func linesFromInput(in []api.InvoiceLineInput) []draftLine {
	out := make([]draftLine, len(in))
	for i, l := range in {
		dl := draftLine{
			Name:              l.Name,
			Description:       value(l.Description),
			Quantity:          decimal.RequireFromString(l.Quantity), // pattern-checked by the contract
			UnitCode:          "C62",
			UnitPrice:         decimal.RequireFromString(l.UnitPrice),
			PriceBaseQuantity: decimal.NewFromInt(1),
			TaxCategory:       "S",
			TaxRate:           decimal.RequireFromString(l.TaxRate),
			PeriodStart:       dateValue(l.PeriodStart),
			PeriodEnd:         dateValue(l.PeriodEnd),
		}
		if l.UnitCode != nil {
			dl.UnitCode = *l.UnitCode
		}
		if l.PriceBaseQuantity != nil {
			dl.PriceBaseQuantity = decimal.RequireFromString(*l.PriceBaseQuantity)
		}
		if l.TaxCategory != nil {
			dl.TaxCategory = string(*l.TaxCategory)
		}
		out[i] = dl
	}
	return out
}

func draftFromRow(inv store.AppInvoice, lines []store.AppInvoiceLine) draft {
	d := draft{
		CustomerID:        inv.CustomerID,
		SeriesID:          inv.SeriesID,
		SupplyDate:        inv.SupplyDate,
		SupplyPeriodStart: inv.SupplyPeriodStart,
		SupplyPeriodEnd:   inv.SupplyPeriodEnd,
		DueDate:           inv.DueDate,
		BuyerReference:    inv.BuyerReference,
		OrderReference:    inv.OrderReference,
		Note:              inv.Note,
		PaymentTerms:      inv.PaymentTerms,
		Prepaid:           inv.PrepaidAmount,
		Lines:             make([]draftLine, len(lines)),
	}
	for i, l := range lines {
		d.Lines[i] = draftLine{
			Name: l.Name, Description: l.Description, Quantity: l.Quantity, UnitCode: l.UnitCode,
			UnitPrice: l.UnitPrice, PriceBaseQuantity: l.PriceBaseQuantity, TaxCategory: l.TaxCategory,
			TaxRate: l.TaxRate, PeriodStart: l.PeriodStart, PeriodEnd: l.PeriodEnd,
		}
	}
	return d
}

// mergeDraft applies a merge patch to d and returns the names of the fields
// whose value changed, in the contract's spelling, for the audit log. The
// contract has already rejected unknown fields and null for customer_id,
// series_id, prepaid_amount, and lines.
func mergeDraft(d *draft, p *api.InvoicePatch) []string {
	var changed []string
	set := func(name string, ok bool) {
		if ok {
			changed = append(changed, name)
		}
	}
	if p.CustomerId != nil {
		set("customer_id", *p.CustomerId != d.CustomerID)
		d.CustomerID = *p.CustomerId
	}
	if p.SeriesId != nil {
		set("series_id", *p.SeriesId != d.SeriesID)
		d.SeriesID = *p.SeriesId
	}
	mergeDate := func(name string, dst **time.Time, n nullable.Nullable[openapi_types.Date]) {
		if n.IsSpecified() {
			v := dateValue(n)
			set(name, !eqDate(*dst, v))
			*dst = v
		}
	}
	mergeDate("supply_date", &d.SupplyDate, p.SupplyDate)
	mergeDate("supply_period_start", &d.SupplyPeriodStart, p.SupplyPeriodStart)
	mergeDate("supply_period_end", &d.SupplyPeriodEnd, p.SupplyPeriodEnd)
	mergeDate("due_date", &d.DueDate, p.DueDate)
	mergeField(&changed, "buyer_reference", &d.BuyerReference, p.BuyerReference)
	mergeField(&changed, "order_reference", &d.OrderReference, p.OrderReference)
	mergeField(&changed, "note", &d.Note, p.Note)
	mergeField(&changed, "payment_terms", &d.PaymentTerms, p.PaymentTerms)
	if p.PrepaidAmount != nil {
		v := decimal.RequireFromString(*p.PrepaidAmount) // pattern-checked by the contract
		set("prepaid_amount", !v.Equal(d.Prepaid))
		d.Prepaid = v
	}
	if p.Lines != nil {
		lines := linesFromInput(*p.Lines)
		set("lines", !slices.EqualFunc(lines, d.Lines, draftLine.equal))
		d.Lines = lines
	}
	return changed
}

// lineRow is one line as InsertInvoiceLines reads it from JSON. Numbers are
// strings, so they reach Postgres exactly.
type lineRow struct {
	Position          int     `json:"position"`
	Name              string  `json:"name"`
	Description       *string `json:"description"`
	Quantity          string  `json:"quantity"`
	UnitCode          string  `json:"unit_code"`
	UnitPrice         string  `json:"unit_price"`
	PriceBaseQuantity string  `json:"price_base_quantity"`
	TaxCategory       string  `json:"tax_category"`
	TaxRate           string  `json:"tax_rate"`
	LineNetAmount     string  `json:"line_net_amount"`
	PeriodStart       *string `json:"period_start"`
	PeriodEnd         *string `json:"period_end"`
}

func writeLines(ctx context.Context, invoiceID uuid.UUID, lines []draftLine, res invoicecalc.Result) error {
	if len(lines) == 0 {
		return nil
	}
	rows := make([]lineRow, len(lines))
	for i, l := range lines {
		rows[i] = lineRow{
			Position: i + 1, Name: l.Name, Description: l.Description,
			Quantity: l.Quantity.String(), UnitCode: l.UnitCode, UnitPrice: l.UnitPrice.String(),
			PriceBaseQuantity: l.PriceBaseQuantity.String(), TaxCategory: l.TaxCategory, TaxRate: l.TaxRate.String(),
			LineNetAmount: res.LineNetAmounts[i].String(),
			PeriodStart:   dateString(l.PeriodStart), PeriodEnd: dateString(l.PeriodEnd),
		}
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	if err := queries(ctx).InsertInvoiceLines(ctx, store.InsertInvoiceLinesParams{InvoiceID: invoiceID, Lines: string(b)}); err != nil {
		return fmt.Errorf("insert lines: %w", err)
	}
	return nil
}

func writeBreakdowns(ctx context.Context, invoiceID uuid.UUID, res invoicecalc.Result) error {
	if len(res.Breakdowns) == 0 {
		return nil
	}
	type row struct {
		TaxCategory   string `json:"tax_category"`
		TaxRate       string `json:"tax_rate"`
		TaxableAmount string `json:"taxable_amount"`
		TaxAmount     string `json:"tax_amount"`
	}
	rows := make([]row, len(res.Breakdowns))
	for i, b := range res.Breakdowns {
		rows[i] = row{b.TaxCategory, b.TaxRate.String(), b.TaxableAmount.String(), b.TaxAmount.String()}
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	if err := queries(ctx).InsertTaxBreakdowns(ctx, store.InsertTaxBreakdownsParams{InvoiceID: invoiceID, Breakdowns: string(data)}); err != nil {
		return fmt.Errorf("insert breakdowns: %w", err)
	}
	return nil
}

func loadInvoice(ctx context.Context, inv store.AppInvoice) (api.Invoice, error) {
	out, err := loadInvoices(ctx, []store.AppInvoice{inv})
	if err != nil {
		return api.Invoice{}, err
	}
	return out[0], nil
}

// loadInvoices reads the lines, breakdowns, parties, and payment sums of
// invs in four queries, whatever the page size, and builds the responses.
func loadInvoices(ctx context.Context, invs []store.AppInvoice) ([]api.Invoice, error) {
	if len(invs) == 0 {
		return []api.Invoice{}, nil
	}
	q := queries(ctx)
	ids := make([]string, len(invs))
	for i, inv := range invs {
		ids[i] = inv.ID.String()
	}
	lines, err := q.ListInvoiceLines(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read lines: %w", err)
	}
	breakdowns, err := q.ListTaxBreakdowns(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read breakdowns: %w", err)
	}
	parties, err := q.ListInvoiceParties(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read parties: %w", err)
	}
	sums, err := q.SumPayments(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("sum payments: %w", err)
	}

	byInvoice := make(map[uuid.UUID]*api.Invoice, len(invs))
	out := make([]api.Invoice, len(invs))
	for i, inv := range invs {
		out[i] = invoiceBody(inv)
		byInvoice[inv.ID] = &out[i]
	}
	minorOf := func(id uuid.UUID) int32 {
		m, _ := invoicecalc.MinorUnits(string(byInvoice[id].Currency))
		return m
	}
	for _, l := range lines {
		b := byInvoice[l.InvoiceID]
		b.Lines = append(b.Lines, api.InvoiceLine{
			Position:          int(l.Position),
			Name:              l.Name,
			Description:       orNull(l.Description),
			Quantity:          l.Quantity.String(),
			UnitCode:          l.UnitCode,
			UnitPrice:         l.UnitPrice.String(),
			PriceBaseQuantity: l.PriceBaseQuantity.String(),
			TaxCategory:       l.TaxCategory,
			TaxRate:           l.TaxRate.String(),
			LineNetAmount:     l.LineNetAmount.StringFixed(minorOf(l.InvoiceID)),
			PeriodStart:       dateOrNull(l.PeriodStart),
			PeriodEnd:         dateOrNull(l.PeriodEnd),
		})
	}
	for _, t := range breakdowns {
		b := byInvoice[t.InvoiceID]
		m := minorOf(t.InvoiceID)
		b.TaxBreakdown = append(b.TaxBreakdown, api.TaxBreakdown{
			TaxCategory:   t.TaxCategory,
			TaxRate:       t.TaxRate.String(),
			TaxableAmount: t.TaxableAmount.StringFixed(m),
			TaxAmount:     t.TaxAmount.StringFixed(m),
		})
	}
	for _, p := range parties {
		b := byInvoice[p.InvoiceID]
		b.Parties = append(b.Parties, partyBody(p))
	}
	for _, s := range sums {
		b := byInvoice[s.InvoiceID]
		paid := s.AmountPaid
		b.AmountPaid = paid.StringFixed(minorOf(s.InvoiceID))
		b.PaymentStatus = paymentStatus(b, paid)
	}
	return out, nil
}

// invoiceBody maps the invoice row; loadInvoices adds the children.
func invoiceBody(inv store.AppInvoice) api.Invoice {
	m, _ := invoicecalc.MinorUnits(inv.Currency)
	amount := func(d decimal.Decimal) string { return d.StringFixed(m) }
	b := api.Invoice{
		Id:                 inv.ID,
		DocumentType:       api.DocumentType(inv.DocumentType),
		Status:             api.InvoiceStatus(inv.Status),
		CustomerId:         inv.CustomerID,
		SeriesId:           inv.SeriesID,
		PrecedingInvoiceId: uuidOrNull(inv.PrecedingInvoiceID),
		Number:             orNull(inv.Number),
		Jurisdiction:       api.Jurisdiction(inv.Jurisdiction),
		Currency:           api.Currency(inv.Currency),
		IssueDate:          dateOrNull(inv.IssueDate),
		SupplyDate:         dateOrNull(inv.SupplyDate),
		SupplyPeriodStart:  dateOrNull(inv.SupplyPeriodStart),
		SupplyPeriodEnd:    dateOrNull(inv.SupplyPeriodEnd),
		DueDate:            dateOrNull(inv.DueDate),
		BuyerReference:     orNull(inv.BuyerReference),
		OrderReference:     orNull(inv.OrderReference),
		Note:               orNull(inv.Note),
		PaymentTerms:       orNull(inv.PaymentTerms),
		TaxRounding:        orNullAs[api.InvoiceTaxRounding](inv.TaxRounding),
		LineNetTotal:       amount(inv.LineNetTotal),
		AllowanceTotal:     amount(inv.AllowanceTotal),
		ChargeTotal:        amount(inv.ChargeTotal),
		TaxExclusiveTotal:  amount(inv.TaxExclusiveTotal),
		TaxTotal:           amount(inv.TaxTotal),
		TaxInclusiveTotal:  amount(inv.TaxInclusiveTotal),
		PrepaidAmount:      amount(inv.PrepaidAmount),
		RoundingAmount:     amount(inv.RoundingAmount),
		AmountDue:          amount(inv.AmountDue),
		AmountPaid:         amount(decimal.Zero),
		Version:            int(inv.Version),
		Lines:              []api.InvoiceLine{},
		TaxBreakdown:       []api.TaxBreakdown{},
		Parties:            []api.InvoiceParty{},
		RetentionUntil:     dateOrNull(inv.RetentionUntil),
		IssuedAt:           timeOrNull(inv.IssuedAt),
		VoidedAt:           timeOrNull(inv.VoidedAt),
		VoidReason:         orNull(inv.VoidReason),
		CorrectedAt:        timeOrNull(inv.CorrectedAt),
		CreatedAt:          inv.CreatedAt.UTC(),
		UpdatedAt:          inv.UpdatedAt.UTC(),
	}
	b.PaymentStatus = paymentStatus(&b, decimal.Zero)
	return b
}

// paymentStatus is derived from the payments (ADR 0007, D9). Drafts and
// credit notes have none.
func paymentStatus(b *api.Invoice, paid decimal.Decimal) nullable.Nullable[api.InvoicePaymentStatus] {
	if b.Status == api.Draft || b.DocumentType == api.DocumentTypeCreditNote {
		return nullable.NewNullNullable[api.InvoicePaymentStatus]()
	}
	due := decimal.RequireFromString(b.AmountDue)
	switch {
	case paid.GreaterThanOrEqual(due):
		return nullable.NewNullableWithValue(api.InvoicePaymentStatusPaid)
	case paid.IsPositive():
		return nullable.NewNullableWithValue(api.InvoicePaymentStatusPartiallyPaid)
	}
	return nullable.NewNullableWithValue(api.InvoicePaymentStatusUnpaid)
}

func partyBody(p store.AppInvoiceParty) api.InvoiceParty {
	return api.InvoiceParty{
		Role:                 api.InvoicePartyRole(p.Role),
		Name:                 orNull(p.Name),
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
		LegalRegistrationId:  orNull(p.LegalRegistrationID),
		JpRegistrationNumber: orNull(p.JpRegistrationNumber),
		ContactName:          orNull(p.ContactName),
		ContactPhone:         orNull(p.ContactPhone),
		ContactEmail:         orNull(p.ContactEmail),
		Iban:                 orNull(p.Iban),
		Bic:                  orNull(p.Bic),
		AccountName:          orNull(p.AccountName),
	}
}

func invoiceFilter(p api.ListInvoicesParams) string {
	var parts []string
	if p.Status != nil {
		parts = append(parts, "status="+string(*p.Status))
	}
	if p.DocumentType != nil {
		parts = append(parts, "document_type="+string(*p.DocumentType))
	}
	if p.CustomerId != nil {
		parts = append(parts, "customer_id="+p.CustomerId.String())
	}
	if p.CreatedAfter != nil {
		parts = append(parts, "created_after="+p.CreatedAfter.UTC().Format(time.RFC3339Nano))
	}
	if p.CreatedBefore != nil {
		parts = append(parts, "created_before="+p.CreatedBefore.UTC().Format(time.RFC3339Nano))
	}
	return listFilter("invoices", parts...)
}

// etagOf is a draft's ETag: its version as a strong entity tag.
func etagOf(version int32) string { return `"` + strconv.Itoa(int(version)) + `"` }

// ifMatch evaluates If-Match against the current version (RFC 9110
// §13.1.1): "*" or any listed strong tag equal to the ETag matches; a weak
// tag never does, because If-Match uses the strong comparison.
func ifMatch(header string, version int32) bool {
	want := etagOf(version)
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for _, tag := range strings.Split(header, ",") {
		if strings.TrimSpace(tag) == want {
			return true
		}
	}
	return false
}

// requireIfMatch answers 428 when If-Match is missing. It runs before
// validateRequests, which would otherwise call a missing required header a
// 400 (ADR 0008, A4).
func requireIfMatch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values("If-Match")) == 0 {
			problem.Write(w, problem.New(problem.PreconditionRequired, RequestID(r.Context()),
				"Send If-Match with the draft's ETag, from the response that returned it."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func preconditionFailed(ctx context.Context, version int32) api.Problem {
	return problem.New(problem.PreconditionFailed, RequestID(ctx),
		"The draft has changed since that ETag; its current ETag is "+etagOf(version)+". Read it again and reapply your change.")
}

func notDraft(ctx context.Context, inv store.AppInvoice) api.Problem {
	return problem.New(problem.InvoiceNotDraft, RequestID(ctx), fmt.Sprintf(
		"The document is %s; only drafts can be changed. Issue a credit note or a corrected invoice instead.", inv.Status))
}

func invoiceNotFound(ctx context.Context) api.Problem {
	return problem.New(problem.NotFound, RequestID(ctx), "No such invoice.")
}

func dateValue(n nullable.Nullable[openapi_types.Date]) *time.Time {
	v, err := n.Get()
	if err != nil { // absent or null
		return nil
	}
	t := v.Time
	return &t
}

func dateOrNull(t *time.Time) nullable.Nullable[openapi_types.Date] {
	if t == nil {
		return nullable.NewNullNullable[openapi_types.Date]()
	}
	return nullable.NewNullableWithValue(openapi_types.Date{Time: *t})
}

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

func timeOrNull(t *time.Time) nullable.Nullable[time.Time] {
	if t == nil {
		return nullable.NewNullNullable[time.Time]()
	}
	return nullable.NewNullableWithValue(t.UTC())
}

func uuidOrNull(id *uuid.UUID) nullable.Nullable[openapi_types.UUID] {
	if id == nil {
		return nullable.NewNullNullable[openapi_types.UUID]()
	}
	return nullable.NewNullableWithValue(*id)
}

func eqPtr[T comparable](a, b *T) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func eqDate(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}
