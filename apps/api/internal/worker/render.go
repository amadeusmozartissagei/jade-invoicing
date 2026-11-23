package worker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/db"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/invoicecalc"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/invoicepdf"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/objstore"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/store"
)

// RenderQueue is the queue issuing a document sends to (ADR 0017, D1).
const RenderQueue = "render_pdf"

// The file this queue produces. The bucket's allowed MIME types (migration
// 20261123120000) must include pdfType.
const (
	pdfKind = "pdf"
	pdfType = "application/pdf"
)

// Storage keys hold the content hash, so an object is never replaced by
// different bytes (ADR 0017, D2).
func pdfKey(tenantID, invoiceID uuid.UUID, sha []byte) string {
	return fmt.Sprintf("%s/%s/pdf/%s.pdf", tenantID, invoiceID, hex.EncodeToString(sha))
}

type renderMessage struct {
	TenantID  string `json:"tenant_id"`
	InvoiceID string `json:"invoice_id"`
}

// RenderPDF returns the handler of RenderQueue:
//
//  1. in a tenant transaction, stop if the document already has a PDF, or
//     else read everything the PDF shows;
//  2. render, outside any transaction;
//  3. upload under the content hash;
//  4. in a second tenant transaction, insert the invoice_files row, keeping
//     an existing one.
//
// Every step is safe to repeat: the render is deterministic, so a retry
// uploads the same bytes to the same key, and the row is inserted once.
func RenderPDF(pool *pgxpool.Pool, log *slog.Logger, r *invoicepdf.Renderer, files *objstore.Store) Handler {
	return func(ctx context.Context, m Message) error {
		var msg renderMessage
		if err := json.Unmarshal(m.Body, &msg); err != nil {
			return fmt.Errorf("decode message: %w", err)
		}
		tenantID, err := uuid.Parse(msg.TenantID)
		if err != nil {
			return fmt.Errorf("tenant_id: %w", err)
		}
		invoiceID, err := uuid.Parse(msg.InvoiceID)
		if err != nil {
			return fmt.Errorf("invoice_id: %w", err)
		}

		var in invoicepdf.Input
		done := false
		err = db.InTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
			q := store.New(tx)
			if _, err := q.GetInvoiceFile(ctx, store.GetInvoiceFileParams{InvoiceID: invoiceID, Kind: pdfKind}); err == nil {
				done = true
				return nil
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("read invoice file: %w", err)
			}
			in, err = renderInput(ctx, q, invoiceID)
			return err
		})
		if err != nil {
			return err
		}
		if done {
			log.Info("pdf already rendered", "invoice_id", invoiceID)
			return nil
		}

		pdf, err := r.Render(ctx, in)
		if err != nil {
			return err
		}
		key := pdfKey(tenantID, invoiceID, pdf.SHA256[:])
		if err := files.Put(ctx, key, pdf.Bytes, pdfType); err != nil {
			return err
		}
		return db.InTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
			_, err := store.New(tx).InsertInvoiceFile(ctx, store.InsertInvoiceFileParams{
				InvoiceID: invoiceID, Kind: pdfKind, StoragePath: key, ContentType: pdfType,
				ByteSize: int64(len(pdf.Bytes)), Sha256: pdf.SHA256[:],
			})
			if err != nil {
				return fmt.Errorf("insert invoice file: %w", err)
			}
			return nil
		})
	}
}

// renderInput reads an issued document and turns it into the renderer's
// input. Only fields fixed at issue are read (ADR 0017, D3).
func renderInput(ctx context.Context, q *store.Queries, id uuid.UUID) (invoicepdf.Input, error) {
	inv, err := q.GetInvoice(ctx, id)
	if err != nil {
		return invoicepdf.Input{}, fmt.Errorf("read invoice: %w", err)
	}
	if inv.Number == nil || inv.IssueDate == nil || inv.IssuedAt == nil {
		return invoicepdf.Input{}, fmt.Errorf("invoice %s is not issued", id)
	}
	ids := []string{id.String()}
	lines, err := q.ListInvoiceLines(ctx, ids)
	if err != nil {
		return invoicepdf.Input{}, fmt.Errorf("read lines: %w", err)
	}
	breakdowns, err := q.ListTaxBreakdowns(ctx, ids)
	if err != nil {
		return invoicepdf.Input{}, fmt.Errorf("read breakdowns: %w", err)
	}
	parties, err := q.ListInvoiceParties(ctx, ids)
	if err != nil {
		return invoicepdf.Input{}, fmt.Errorf("read parties: %w", err)
	}
	minor, ok := invoicecalc.MinorUnits(inv.Currency)
	if !ok {
		return invoicepdf.Input{}, fmt.Errorf("unknown currency %q", inv.Currency)
	}

	in := invoicepdf.Input{
		Jurisdiction:      inv.Jurisdiction,
		DocumentType:      inv.DocumentType,
		Number:            *inv.Number,
		IssueDate:         *inv.IssueDate,
		IssuedAt:          *inv.IssuedAt,
		SupplyDate:        inv.SupplyDate,
		SupplyPeriodStart: inv.SupplyPeriodStart,
		SupplyPeriodEnd:   inv.SupplyPeriodEnd,
		DueDate:           inv.DueDate,
		BuyerReference:    inv.BuyerReference,
		OrderReference:    inv.OrderReference,
		PaymentTerms:      inv.PaymentTerms,
		Note:              inv.Note,
		Currency:          inv.Currency,
		MinorUnits:        minor,
		TaxExclusiveTotal: inv.TaxExclusiveTotal,
		TaxTotal:          inv.TaxTotal,
		TaxInclusiveTotal: inv.TaxInclusiveTotal,
		PrepaidAmount:     inv.PrepaidAmount,
		AmountDue:         inv.AmountDue,
	}
	for _, l := range lines {
		in.Lines = append(in.Lines, invoicepdf.Line{
			Name: l.Name, Description: str(l.Description), Quantity: l.Quantity, UnitCode: l.UnitCode,
			UnitPrice: l.UnitPrice, PriceBaseQuantity: l.PriceBaseQuantity, TaxRate: l.TaxRate, NetAmount: l.LineNetAmount,
			PeriodStart: l.PeriodStart, PeriodEnd: l.PeriodEnd,
		})
	}
	for _, b := range breakdowns {
		in.Breakdown = append(in.Breakdown, invoicepdf.TaxSubtotal{
			TaxRate: b.TaxRate, TaxableAmount: b.TaxableAmount, TaxAmount: b.TaxAmount,
		})
	}
	for _, p := range parties {
		party := toParty(p)
		switch p.Role {
		case "seller":
			in.Seller = party
		case "buyer":
			in.Buyer = party
		}
	}
	if inv.PrecedingInvoiceID != nil {
		prev, err := q.GetInvoice(ctx, *inv.PrecedingInvoiceID)
		if err != nil {
			return invoicepdf.Input{}, fmt.Errorf("read preceding invoice: %w", err)
		}
		if prev.Number == nil || prev.IssueDate == nil {
			return invoicepdf.Input{}, fmt.Errorf("preceding invoice %s is not issued", prev.ID)
		}
		in.Preceding = &invoicepdf.Reference{
			Number: *prev.Number, IssueDate: *prev.IssueDate, SupplyDate: prev.SupplyDate,
			SupplyPeriodStart: prev.SupplyPeriodStart, SupplyPeriodEnd: prev.SupplyPeriodEnd,
		}
	}
	return in, nil
}

func toParty(p store.AppInvoiceParty) invoicepdf.Party {
	var addr []string
	for _, a := range []*string{p.AddressLine1, p.AddressLine2, p.AddressLine3} {
		if s := str(a); s != "" {
			addr = append(addr, s)
		}
	}
	return invoicepdf.Party{
		Name: str(p.Name), AddressLines: addr, PostCode: str(p.PostCode), City: str(p.City),
		CountryCode: str(p.CountryCode), VatID: str(p.VatID), TaxNumber: str(p.TaxNumber),
		JpRegistrationNumber: str(p.JpRegistrationNumber),
		IBAN:                 str(p.Iban), BIC: str(p.Bic), AccountName: str(p.AccountName),
	}
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
