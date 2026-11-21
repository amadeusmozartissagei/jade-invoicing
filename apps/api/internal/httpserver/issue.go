package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/api"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/invoicecalc"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/problem"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/store"
)

// IssueInvoice issues a draft (data-model.md, "Invoice lifecycle"; ADR 0016).
func (h *handlers) IssueInvoice(ctx context.Context, req api.IssueInvoiceRequestObject) (api.IssueInvoiceResponseObject, error) {
	out, err := issueDraft(ctx, req.Id, req.Params.IfMatch, issueOptions{})
	if err != nil {
		return nil, err
	}
	if p := out.problem; p != nil {
		switch p.Status {
		case 404:
			return api.IssueInvoice404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: api.NotFoundApplicationProblemPlusJSONResponse{Body: *p},
			}, nil
		case 412:
			return api.IssueInvoice412ApplicationProblemPlusJSONResponse{
				PreconditionFailedApplicationProblemPlusJSONResponse: api.PreconditionFailedApplicationProblemPlusJSONResponse{Body: *p},
			}, nil
		default:
			return api.IssueInvoice409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: api.ConflictApplicationProblemPlusJSONResponse{Body: *p},
			}, nil
		}
	}
	return api.IssueInvoice200JSONResponse{Body: out.body}, nil
}

// VoidInvoice issues a credit note for the full amount and marks the
// original voided, in one transaction (ADR 0007, D4).
func (h *handlers) VoidInvoice(ctx context.Context, req api.VoidInvoiceRequestObject) (api.VoidInvoiceResponseObject, error) {
	q := queries(ctx)
	conflict := func(p api.Problem) api.VoidInvoiceResponseObject {
		return api.VoidInvoice409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse: api.ConflictApplicationProblemPlusJSONResponse{Body: p},
		}
	}

	rows, err := q.LockInvoices(ctx, []string{req.Id.String()})
	if err != nil {
		return nil, fmt.Errorf("lock invoice: %w", err)
	}
	if len(rows) == 0 {
		return api.VoidInvoice404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: api.NotFoundApplicationProblemPlusJSONResponse{Body: invoiceNotFound(ctx)},
		}, nil
	}
	orig := rows[0]
	if !creditable(orig) {
		return conflict(problem.New(problem.InvoiceNotIssued, RequestID(ctx), fmt.Sprintf(
			"The document is a %s %s; only an issued invoice or corrected invoice can be voided.", orig.Status, orig.DocumentType))), nil
	}
	// The credit note mirrors the original: same customer, series, dates,
	// and lines. If credit notes already exist, issuing it fails with
	// credit_exceeds_invoice and the whole void rolls back.
	lines, err := q.ListInvoiceLines(ctx, []string{orig.ID.String()})
	if err != nil {
		return nil, fmt.Errorf("read lines: %w", err)
	}
	src := draftFromRow(orig, lines)
	note := "Voids invoice " + *orig.Number + ": " + req.Body.Reason
	cn, err := q.CreateInvoice(ctx, store.CreateInvoiceParams{
		DocumentType:       "credit_note",
		CustomerID:         orig.CustomerID,
		SeriesID:           orig.SeriesID,
		PrecedingInvoiceID: &orig.ID,
		Jurisdiction:       orig.Jurisdiction,
		Currency:           orig.Currency,
		SupplyDate:         orig.SupplyDate,
		SupplyPeriodStart:  orig.SupplyPeriodStart,
		SupplyPeriodEnd:    orig.SupplyPeriodEnd,
		BuyerReference:     orig.BuyerReference,
		OrderReference:     orig.OrderReference,
		Note:               &note,
		PrepaidAmount:      decimal.Zero,
	})
	if err != nil {
		return nil, fmt.Errorf("create credit note: %w", err)
	}
	minor, _ := invoicecalc.MinorUnits(orig.Currency)
	res, err := invoicecalc.Calculate(calcLines(src.Lines), invoicecalc.Params{
		MinorUnits: minor, Rounding: invoicecalc.Rounding(*orig.TaxRounding),
	})
	if err != nil {
		return nil, fmt.Errorf("compute credit note: %w", err)
	}
	if err := writeLines(ctx, cn.ID, src.Lines, res); err != nil {
		return nil, err
	}
	if err := audit(ctx, "invoice.create", auditInvoice, cn.ID, nil); err != nil {
		return nil, err
	}

	out, err := issueDraft(ctx, cn.ID, nil, issueOptions{rounding: invoicecalc.Rounding(*orig.TaxRounding), partiesOf: &orig.ID})
	if err != nil {
		return nil, err
	}
	if out.problem != nil {
		// The copy of an issued invoice is complete by construction; the
		// only way here is a rate made invalid by a later migration.
		return conflict(*out.problem), nil
	}

	if n, err := q.MarkInvoiceVoided(ctx, store.MarkInvoiceVoidedParams{ID: orig.ID, VoidReason: req.Body.Reason}); err != nil || n != 1 {
		return nil, fmt.Errorf("mark voided: %d rows, %w", n, err)
	}
	voided, err := q.GetInvoice(ctx, orig.ID)
	if err != nil {
		return nil, fmt.Errorf("read voided invoice: %w", err)
	}
	voidedBody, err := loadInvoice(ctx, voided)
	if err != nil {
		return nil, err
	}
	if err := emitEvent(ctx, api.InvoiceVoided, voidedBody); err != nil {
		return nil, err
	}
	if err := audit(ctx, "invoice.void", auditInvoice, orig.ID, nil); err != nil {
		return nil, err
	}

	return api.VoidInvoice200JSONResponse{Body: api.VoidResult{VoidedInvoice: voidedBody, CreditNote: out.body}}, nil
}

// issueOptions are set by void, whose credit note mirrors the original.
type issueOptions struct {
	rounding  invoicecalc.Rounding // the original's rounding, not the tenant's current one
	partiesOf *uuid.UUID           // copy the parties of this issued document
}

type issueResult struct {
	issued  store.AppInvoice
	body    api.Invoice // the issued document as the API returns it
	problem *api.Problem
}

// issueDraft runs the issue transaction (data-model.md, "Invoice
// numbering"): lock, check, compute, number, copy the parties, mark issued,
// and write the events, all in the request's transaction. Any problem or
// error rolls all of it back, so a failed issue uses no number.
func issueDraft(ctx context.Context, id uuid.UUID, ifMatchTag *string, opts issueOptions) (issueResult, error) {
	q := queries(ctx)
	t := tenantFrom(ctx).tenant
	fail := func(kind problem.Kind, detail string) (issueResult, error) {
		p := problem.New(kind, RequestID(ctx), detail)
		return issueResult{problem: &p}, nil
	}

	// Lock order: invoice rows first, in id order, then the number series.
	head, err := q.GetInvoice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		p := invoiceNotFound(ctx)
		return issueResult{problem: &p}, nil
	}
	if err != nil {
		return issueResult{}, fmt.Errorf("read invoice: %w", err)
	}
	ids := []string{id.String()}
	if head.PrecedingInvoiceID != nil {
		ids = append(ids, head.PrecedingInvoiceID.String())
	}
	rows, err := q.LockInvoices(ctx, ids)
	if err != nil {
		return issueResult{}, fmt.Errorf("lock invoices: %w", err)
	}
	var inv store.AppInvoice
	var orig *store.AppInvoice
	for i := range rows {
		if rows[i].ID == id {
			inv = rows[i]
		} else {
			orig = &rows[i]
		}
	}

	if inv.ID == uuid.Nil { // deleted between the read and the lock
		p := invoiceNotFound(ctx)
		return issueResult{problem: &p}, nil
	}
	if ifMatchTag != nil && !ifMatch(*ifMatchTag, inv.Version) {
		p := preconditionFailed(ctx, inv.Version)
		return issueResult{problem: &p}, nil
	}
	if inv.Status != "draft" {
		p := notDraft(ctx, inv)
		return issueResult{problem: &p}, nil
	}

	// The preceding document must still be open to credit or correction.
	if orig != nil {
		if inv.DocumentType == "corrected_invoice" && orig.Status == "corrected" {
			return fail(problem.CorrectionNotAllowed, "Another correction of this invoice has already been issued.")
		}
		if !creditable(*orig) {
			return fail(problem.InvoiceNotIssued, fmt.Sprintf(
				"The preceding document is a %s %s; only an issued invoice or corrected invoice can be credited or corrected.",
				orig.Status, orig.DocumentType))
		}
	}
	var credited decimal.Decimal
	if orig != nil {
		if credited, err = q.CreditedTotal(ctx, orig.ID); err != nil {
			return issueResult{}, fmt.Errorf("sum credit notes: %w", err)
		}
	}
	if orig != nil && inv.DocumentType == "corrected_invoice" {
		payments, err := q.CountPayments(ctx, orig.ID)
		if err != nil {
			return issueResult{}, fmt.Errorf("count payments: %w", err)
		}
		if payments > 0 || credited.IsPositive() {
			return fail(problem.CorrectionNotAllowed,
				"The invoice already has payments or credit notes; correcting it is not supported yet. Credit it instead.")
		}
	}

	// Completeness and the final computation.
	lines, err := q.ListInvoiceLines(ctx, []string{id.String()})
	if err != nil {
		return issueResult{}, fmt.Errorf("read lines: %w", err)
	}
	d := draftFromRow(inv, lines)
	var errs fieldErrors
	var seller store.AppSellerProfile
	var buyer store.AppCustomer
	if opts.partiesOf == nil {
		if seller, err = q.GetSellerProfile(ctx); err != nil {
			return issueResult{}, fmt.Errorf("read seller profile: %w", err)
		}
		if buyer, err = q.GetCustomer(ctx, inv.CustomerID); err != nil {
			return issueResult{}, fmt.Errorf("read customer: %w", err)
		}
		partiesComplete(t, seller, buyer, &errs)
	}
	draftComplete(d, &errs)
	rounding := taxRounding(t)
	if opts.rounding != "" {
		rounding = opts.rounding
	}
	res, err := checkDraft(ctx, t, rounding, &d, true, &errs)
	if err != nil {
		return issueResult{}, err
	}
	if len(errs) > 0 {
		p := errs.problem(ctx, problem.InvoiceIncomplete)
		return issueResult{problem: &p}, nil
	}
	if orig != nil && inv.DocumentType == "credit_note" && credited.Add(res.TaxInclusiveTotal).GreaterThan(orig.TaxInclusiveTotal) {
		return fail(problem.CreditExceedsInvoice, fmt.Sprintf(
			"Credit notes for this invoice would total %s %s, more than its %s %s.",
			credited.Add(res.TaxInclusiveTotal).StringFixed(minorUnits(t)), t.Currency,
			orig.TaxInclusiveTotal.StringFixed(minorUnits(t)), t.Currency))
	}

	if n, err := q.SetDraftTotals(ctx, store.SetDraftTotalsParams{
		ID: id, LineNetTotal: res.LineNetTotal, TaxExclusiveTotal: res.TaxExclusiveTotal, TaxTotal: res.TaxTotal,
		TaxInclusiveTotal: res.TaxInclusiveTotal, AmountDue: res.AmountDue,
	}); err != nil || n != 1 {
		return issueResult{}, fmt.Errorf("set totals: %d rows, %w", n, err)
	}
	if err := q.DeleteTaxBreakdowns(ctx, id); err != nil {
		return issueResult{}, fmt.Errorf("delete breakdowns: %w", err)
	}
	if err := writeBreakdowns(ctx, id, res); err != nil {
		return issueResult{}, err
	}

	if opts.partiesOf != nil {
		if err := copyParties(ctx, *opts.partiesOf, id); err != nil {
			return issueResult{}, err
		}
	} else {
		if err := q.InsertParty(ctx, sellerParty(id, seller)); err != nil {
			return issueResult{}, fmt.Errorf("insert seller: %w", err)
		}
		if err := q.InsertParty(ctx, buyerParty(id, buyer)); err != nil {
			return issueResult{}, fmt.Errorf("insert buyer: %w", err)
		}
	}

	next, err := q.TakeNextNumber(ctx, inv.SeriesID)
	if err != nil {
		return issueResult{}, fmt.Errorf("take number: %w", err)
	}
	issueDate, err := tenantToday(t)
	if err != nil {
		return issueResult{}, err
	}
	if n, err := q.MarkInvoiceIssued(ctx, store.MarkInvoiceIssuedParams{
		ID:             id,
		SequenceValue:  next.SequenceValue,
		Number:         next.Number,
		IssueDate:      issueDate,
		RetentionUntil: retentionUntil(t, issueDate),
		TaxRounding:    string(rounding),
	}); err != nil || n != 1 {
		return issueResult{}, fmt.Errorf("mark issued: %d rows, %w", n, err)
	}
	issued, err := q.GetInvoice(ctx, id)
	if err != nil {
		return issueResult{}, fmt.Errorf("read issued invoice: %w", err)
	}

	eventType := api.InvoiceIssued
	if issued.DocumentType == "credit_note" {
		eventType = api.CreditNoteIssued
	}
	body, err := loadInvoice(ctx, issued)
	if err != nil {
		return issueResult{}, err
	}
	if err := emitEvent(ctx, eventType, body); err != nil {
		return issueResult{}, err
	}
	if err := audit(ctx, "invoice.issue", auditInvoice, id, nil); err != nil {
		return issueResult{}, err
	}

	// A corrected invoice replaces its original (ADR 0007, D10).
	if orig != nil && issued.DocumentType == "corrected_invoice" {
		if n, err := q.MarkInvoiceCorrected(ctx, orig.ID); err != nil || n != 1 {
			return issueResult{}, fmt.Errorf("mark corrected: %d rows, %w", n, err)
		}
		corrected, err := q.GetInvoice(ctx, orig.ID)
		if err != nil {
			return issueResult{}, fmt.Errorf("read corrected invoice: %w", err)
		}
		correctedBody, err := loadInvoice(ctx, corrected)
		if err != nil {
			return issueResult{}, err
		}
		if err := emitEvent(ctx, api.InvoiceCorrected, correctedBody); err != nil {
			return issueResult{}, err
		}
		if err := audit(ctx, "invoice.correct", auditInvoice, orig.ID, nil); err != nil {
			return issueResult{}, err
		}
	}
	return issueResult{issued: issued, body: body}, nil
}

// partiesComplete adds what the seller profile and the customer lack to
// issue (ADR 0016, I3):
//
//   - DE, § 14 Abs. 4 Nr. 1 and 2 UStG [UStG-14]: full name and address of
//     both parties, and the seller's VAT ID or tax number.
//   - JP, the qualified invoice's items 1 and 6 [NTA-EN]: the issuer's name
//     and registration number, and the recipient's name (always present).
func partiesComplete(t store.AppTenant, s store.AppSellerProfile, c store.AppCustomer, errs *fieldErrors) {
	present := func(v *string) bool { return v != nil && *v != "" }
	need := func(ptr string, v *string, what string) {
		if !present(v) {
			errs.add(ptr, codeRequired, what+" is required to issue.")
		}
	}
	need("/seller_profile/legal_name", s.LegalName, "The seller's legal name")
	if t.Jurisdiction == "JP" {
		need("/seller_profile/jp_registration_number", s.JpRegistrationNumber, "The qualified invoice issuer registration number")
		return
	}
	need("/seller_profile/address_line1", s.AddressLine1, "The seller's street address")
	need("/seller_profile/city", s.City, "The seller's city")
	need("/seller_profile/post_code", s.PostCode, "The seller's post code")
	need("/seller_profile/country_code", s.CountryCode, "The seller's country")
	if !present(s.VatID) && !present(s.TaxNumber) {
		errs.add("/seller_profile/vat_id", codeRequired, "The seller's VAT ID or tax number is required to issue.")
	}
	need("/customer/address_line1", c.AddressLine1, "The customer's street address")
	need("/customer/city", c.City, "The customer's city")
	need("/customer/post_code", c.PostCode, "The customer's post code")
	need("/customer/country_code", c.CountryCode, "The customer's country")
}

// draftComplete adds what the draft itself lacks: the date of supply (a date,
// or a period with both ends) and at least one line.
func draftComplete(d draft, errs *fieldErrors) {
	if d.SupplyDate == nil && (d.SupplyPeriodStart == nil || d.SupplyPeriodEnd == nil) {
		errs.add("/supply_date", codeRequired, "A supply date, or a supply period with start and end, is required to issue.")
	}
	if len(d.Lines) == 0 {
		errs.add("/lines", codeRequired, "At least one line is required to issue.")
	}
}

// retentionUntil is the earliest date an issued document may be deleted
// (data-model.md, "Retention"):
//
//   - DE: 8 years from the end of the calendar year of issue [UStG-14b].
//   - JP: 7 years from the day after two months have passed since the end of
//     the taxable period of issue [NTA-LEAFLET p.11]; the taxable period ends
//     with the tenant's fiscal_year_end_month.
func retentionUntil(t store.AppTenant, issued time.Time) time.Time {
	if t.Jurisdiction != "JP" {
		return time.Date(issued.Year()+8, time.December, 31, 0, 0, 0, 0, time.UTC)
	}
	end := time.Month(t.FiscalYearEndMonth)
	year := issued.Year()
	if issued.Month() > end {
		year++
	}
	// Two months after the period's end, then seven years. The last day of
	// a month is taken in the target year, so February is never overrun.
	deadline := time.Date(year, end+3, 1, 0, 0, 0, 0, time.UTC) // first day after the two months
	return time.Date(deadline.Year()+7, deadline.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
}

func minorUnits(t store.AppTenant) int32 {
	m, _ := invoicecalc.MinorUnits(t.Currency)
	return m
}

func calcLines(lines []draftLine) []invoicecalc.Line {
	in := make([]invoicecalc.Line, len(lines))
	for i, l := range lines {
		in[i] = invoicecalc.Line{
			Quantity: l.Quantity, UnitPrice: l.UnitPrice, PriceBaseQuantity: l.PriceBaseQuantity,
			TaxCategory: l.TaxCategory, TaxRate: l.TaxRate,
		}
	}
	return in
}

func sellerParty(invoiceID uuid.UUID, s store.AppSellerProfile) store.InsertPartyParams {
	return store.InsertPartyParams{
		InvoiceID: invoiceID, Role: "seller", Name: s.LegalName, TradingName: s.TradingName,
		AddressLine1: s.AddressLine1, AddressLine2: s.AddressLine2, AddressLine3: s.AddressLine3,
		City: s.City, PostCode: s.PostCode, CountrySubdivision: s.CountrySubdivision, CountryCode: s.CountryCode,
		VatID: s.VatID, TaxNumber: s.TaxNumber, JpRegistrationNumber: s.JpRegistrationNumber,
		ContactName: s.ContactName, ContactPhone: s.ContactPhone, ContactEmail: s.ContactEmail,
		Iban: s.Iban, Bic: s.Bic, AccountName: s.AccountName,
	}
}

func buyerParty(invoiceID uuid.UUID, c store.AppCustomer) store.InsertPartyParams {
	return store.InsertPartyParams{
		InvoiceID: invoiceID, Role: "buyer", Name: &c.Name, TradingName: c.TradingName,
		AddressLine1: c.AddressLine1, AddressLine2: c.AddressLine2, AddressLine3: c.AddressLine3,
		City: c.City, PostCode: c.PostCode, CountrySubdivision: c.CountrySubdivision, CountryCode: c.CountryCode,
		VatID: c.VatID, LegalRegistrationID: c.LegalRegistrationID,
		ContactName: c.ContactName, ContactPhone: c.ContactPhone, ContactEmail: c.ContactEmail,
	}
}

// copyParties gives a void's credit note the parties of the invoice it
// cancels, so it names the same seller and buyer whatever changed since.
func copyParties(ctx context.Context, from, to uuid.UUID) error {
	q := queries(ctx)
	parties, err := q.ListInvoiceParties(ctx, []string{from.String()})
	if err != nil {
		return fmt.Errorf("read parties: %w", err)
	}
	for _, p := range parties {
		if err := q.InsertParty(ctx, store.InsertPartyParams{
			InvoiceID: to, Role: p.Role, Name: p.Name, TradingName: p.TradingName,
			AddressLine1: p.AddressLine1, AddressLine2: p.AddressLine2, AddressLine3: p.AddressLine3,
			City: p.City, PostCode: p.PostCode, CountrySubdivision: p.CountrySubdivision, CountryCode: p.CountryCode,
			VatID: p.VatID, TaxNumber: p.TaxNumber, LegalRegistrationID: p.LegalRegistrationID,
			JpRegistrationNumber: p.JpRegistrationNumber, ContactName: p.ContactName, ContactPhone: p.ContactPhone,
			ContactEmail: p.ContactEmail, Iban: p.Iban, Bic: p.Bic, AccountName: p.AccountName,
		}); err != nil {
			return fmt.Errorf("copy %s: %w", p.Role, err)
		}
	}
	return nil
}

// fanOut is the outbox's second half (data-model.md, "Events and
// webhooks"): one delivery per enabled endpoint subscribed to the event, and
// one pgmq message per delivery, in the same transaction as the change. The
// messages carry only IDs, because pgmq's tables have no RLS. Written by hand
// because sqlc does not know pgmq's functions.
const fanOut = `
with d as (
  insert into app.webhook_deliveries (tenant_id, event_id, endpoint_id, next_attempt_at)
  select e.tenant_id, $1::uuid, e.id, now()
  from app.webhook_endpoints e
  where e.enabled and (cardinality(e.event_types) = 0 or $2::text = any(e.event_types))
  returning id, tenant_id
)
select count(*)
from d
cross join lateral pgmq.send('webhook_deliveries', jsonb_build_object('delivery_id', d.id, 'tenant_id', d.tenant_id)) as m`

// emitEvent writes the event with its webhook payload (ADR 0009, W2: no
// personal data) and fans it out to the subscribed endpoints.
func emitEvent(ctx context.Context, typ api.EventType, body api.Invoice) error {
	ev := api.Event{
		Type:      typ,
		Timestamp: time.Now().UTC().Truncate(time.Millisecond),
		Data: api.EventInvoice{
			Object:             api.EventInvoiceObjectInvoice,
			Id:                 body.Id,
			DocumentType:       body.DocumentType,
			Status:             body.Status,
			Number:             body.Number.MustGet(),
			CustomerId:         body.CustomerId,
			PrecedingInvoiceId: body.PrecedingInvoiceId,
			IssueDate:          body.IssueDate.MustGet(),
			Currency:           body.Currency,
			TaxInclusiveTotal:  body.TaxInclusiveTotal,
			AmountDue:          body.AmountDue,
			AmountPaid:         body.AmountPaid,
		},
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	q := queries(ctx)
	eventID, err := q.InsertEvent(ctx, store.InsertEventParams{Type: string(typ), SubjectID: body.Id, Payload: string(payload)})
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	var n int64
	if err := tenantFrom(ctx).tx.QueryRow(ctx, fanOut, eventID.String(), string(typ)).Scan(&n); err != nil {
		return fmt.Errorf("fan out %s: %w", typ, err)
	}
	return nil
}
