-- Invoices, credit notes, and corrected invoices (data-model.md, "invoices").
-- Drafts are edited freely; the triggers in 20261102120200_app_invariants.sql
-- keep issued documents and their children unchanged whatever Go does.

-- name: GetSeriesID :one
-- A series of the current tenant, by id; RLS hides other tenants' series.
select id from app.number_series where id = @id;

-- name: GetDefaultSeriesID :one
-- Every tenant has one, created by a trigger with the tenant.
select id from app.number_series where code = 'default';

-- name: RateIsValid :one
-- A line's rate must be valid for the jurisdiction on the supply date
-- (data-model.md, "tax_rates").
select exists (
  select 1 from app.tax_rates
  where jurisdiction = @jurisdiction
    and tax_category = @tax_category
    and rate = @rate
    and valid_from <= @on_date::date
    and (valid_to is null or valid_to >= @on_date::date)
)::boolean as valid;

-- name: CreateInvoice :one
insert into app.invoices (
  tenant_id, document_type, customer_id, series_id, preceding_invoice_id,
  jurisdiction, currency,
  supply_date, supply_period_start, supply_period_end, due_date,
  buyer_reference, order_reference, note, payment_terms,
  line_net_total, tax_exclusive_total, tax_total, tax_inclusive_total, prepaid_amount, amount_due
) values (
  app.current_tenant_id(), @document_type, @customer_id, @series_id, @preceding_invoice_id,
  @jurisdiction, @currency,
  @supply_date, @supply_period_start, @supply_period_end, @due_date,
  @buyer_reference, @order_reference, @note, @payment_terms,
  @line_net_total, @tax_exclusive_total, @tax_total, @tax_inclusive_total, @prepaid_amount, @amount_due
)
returning *;

-- name: GetInvoice :one
select * from app.invoices where id = @id;

-- name: GetInvoiceForUpdate :one
-- PATCH and DELETE lock the draft, then compare its version with If-Match,
-- so two edits made from the same ETag cannot both apply (ADR 0008, A4).
select * from app.invoices where id = @id for update;

-- name: UpdateDraft :one
-- Writes the whole draft after a merge in Go, and moves the version on.
update app.invoices set
  customer_id         = @customer_id,
  series_id           = @series_id,
  supply_date         = @supply_date,
  supply_period_start = @supply_period_start,
  supply_period_end   = @supply_period_end,
  due_date            = @due_date,
  buyer_reference     = @buyer_reference,
  order_reference     = @order_reference,
  note                = @note,
  payment_terms       = @payment_terms,
  line_net_total      = @line_net_total,
  tax_exclusive_total = @tax_exclusive_total,
  tax_total           = @tax_total,
  tax_inclusive_total = @tax_inclusive_total,
  prepaid_amount      = @prepaid_amount,
  amount_due          = @amount_due,
  version             = version + 1
where id = @id
  and status = 'draft'
returning *;

-- name: DeleteDraft :execrows
delete from app.invoices where id = @id and status = 'draft';

-- name: InsertInvoiceLines :exec
-- All lines in one statement, sent as a JSON array. Amounts are JSON strings
-- cast to numeric by Postgres, so no float is ever involved.
insert into app.invoice_lines (
  tenant_id, invoice_id, position, name, description, quantity, unit_code, unit_price,
  price_base_quantity, tax_category, tax_rate, line_net_amount, period_start, period_end
)
select
  app.current_tenant_id(), @invoice_id, l.position, l.name, l.description, l.quantity, l.unit_code, l.unit_price,
  l.price_base_quantity, l.tax_category, l.tax_rate, l.line_net_amount, l.period_start, l.period_end
from jsonb_to_recordset((@lines::text)::jsonb) as l (
  position smallint, name text, description text, quantity numeric, unit_code text, unit_price numeric,
  price_base_quantity numeric, tax_category text, tax_rate numeric, line_net_amount numeric,
  period_start date, period_end date
);

-- name: DeleteInvoiceLines :exec
delete from app.invoice_lines where invoice_id = @invoice_id;

-- name: InsertTaxBreakdowns :exec
insert into app.invoice_tax_breakdowns (tenant_id, invoice_id, tax_category, tax_rate, taxable_amount, tax_amount)
select app.current_tenant_id(), @invoice_id, b.tax_category, b.tax_rate, b.taxable_amount, b.tax_amount
from jsonb_to_recordset((@breakdowns::text)::jsonb)
  as b (tax_category text, tax_rate numeric, taxable_amount numeric, tax_amount numeric);

-- name: DeleteTaxBreakdowns :exec
delete from app.invoice_tax_breakdowns where invoice_id = @invoice_id;

-- Children of a page of invoices. IDs travel as text[]: in exec mode pgx
-- cannot encode a []uuid.UUID without knowing the parameter type.
-- name: ListInvoiceLines :many
select * from app.invoice_lines where invoice_id = any((@invoice_ids::text[])::uuid[]) order by invoice_id, position;

-- name: ListTaxBreakdowns :many
-- Standard rate first, as the tax engine orders them.
select * from app.invoice_tax_breakdowns
where invoice_id = any((@invoice_ids::text[])::uuid[])
order by invoice_id, tax_category, tax_rate desc;

-- name: ListInvoiceParties :many
select * from app.invoice_parties where invoice_id = any((@invoice_ids::text[])::uuid[]) order by invoice_id, role desc;

-- name: SumPayments :many
select invoice_id, sum(amount)::numeric as amount_paid
from app.payments
where invoice_id = any((@invoice_ids::text[])::uuid[])
group by invoice_id;

-- name: ListInvoices :many
-- Keyset pagination on (created_at, id), newest first (ADR 0008, A6). Ask
-- for one row more than the page to know whether there is a next page.
select * from app.invoices
where (sqlc.narg(status)::text is null or status = sqlc.narg(status))
  and (sqlc.narg(document_type)::text is null or document_type = sqlc.narg(document_type))
  and (sqlc.narg(customer_id)::uuid is null or customer_id = sqlc.narg(customer_id))
  and (sqlc.narg(created_after)::timestamptz is null or created_at >= sqlc.narg(created_after))
  and (sqlc.narg(created_before)::timestamptz is null or created_at < sqlc.narg(created_before))
  and (sqlc.narg(after_created_at)::timestamptz is null
       or (created_at, id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
order by created_at desc, id desc
limit @row_limit;
