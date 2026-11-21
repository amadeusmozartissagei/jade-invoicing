-- Issuing, voiding, and correcting documents (data-model.md, "Invoice
-- lifecycle", "Invoice numbering"; ADR 0016). Every path locks invoice rows
-- first, in id order, then the number series, so no two paths can deadlock.

-- name: LockInvoices :many
-- Locks the document being issued and the one it refers to, in id order.
select * from app.invoices
where id = any((@ids::text[])::uuid[])
order by id
for update;

-- name: CreditedTotal :one
-- What the issued credit notes for an invoice already credit. The invoice is
-- locked by the caller, so no other credit note can be issued meanwhile.
select coalesce(sum(tax_inclusive_total), 0)::numeric as credited
from app.invoices
where preceding_invoice_id = @invoice_id::uuid
  and document_type = 'credit_note'
  and status <> 'draft';

-- name: CountPayments :one
select count(*)::bigint from app.payments where invoice_id = @invoice_id;

-- name: SetDraftTotals :execrows
-- The totals as computed at issue, with the rounding that is then recorded.
update app.invoices set
  line_net_total      = @line_net_total,
  tax_exclusive_total = @tax_exclusive_total,
  tax_total           = @tax_total,
  tax_inclusive_total = @tax_inclusive_total,
  amount_due          = @amount_due
where id = @id
  and status = 'draft';

-- name: InsertParty :exec
-- The seller's or buyer's details as they are at issue (ADR 0007, D5).
insert into app.invoice_parties (
  tenant_id, invoice_id, role, name, trading_name,
  address_line1, address_line2, address_line3, city, post_code, country_subdivision, country_code,
  vat_id, tax_number, legal_registration_id, jp_registration_number,
  contact_name, contact_phone, contact_email, iban, bic, account_name
) values (
  app.current_tenant_id(), @invoice_id, @role, @name, @trading_name,
  @address_line1, @address_line2, @address_line3, @city, @post_code, @country_subdivision, @country_code,
  @vat_id, @tax_number, @legal_registration_id, @jp_registration_number,
  @contact_name, @contact_phone, @contact_email, @iban, @bic, @account_name
);

-- name: MarkInvoiceCorrected :execrows
update app.invoices
set status = 'corrected', corrected_at = now()
where id = @id
  and status = 'issued';

-- name: MarkInvoiceVoided :execrows
update app.invoices
set status = 'voided', voided_at = now(), void_reason = @void_reason::text
where id = @id
  and status = 'issued';

-- name: InsertEvent :one
-- The transactional outbox (data-model.md, "Events and webhooks"). The
-- payload is the webhook body and holds no personal data (ADR 0009, W2).
insert into app.events (tenant_id, type, subject_type, subject_id, payload)
values (app.current_tenant_id(), @type, 'invoice', @subject_id, (@payload::text)::jsonb)
returning id;
