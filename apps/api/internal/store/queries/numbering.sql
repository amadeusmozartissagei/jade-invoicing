-- name: LockInvoice :one
-- Step 1 of the issue transaction (data-model.md, "Invoice numbering"). Lock
-- order: invoices first (in id order when there are several), then the number
-- series, so no two paths can deadlock each other.
select id, status, document_type, series_id, preceding_invoice_id, version
from app.invoices
where id = @id
for update;

-- name: TakeNextNumber :one
-- Step 2. The series row stays locked until commit, so issues on one series
-- queue up here, and a rollback also undoes the increment: no gaps, no
-- duplicates.
update app.number_series
set next_value = next_value + 1
where id = @series_id
returning
  (next_value - 1)::bigint as sequence_value,
  (prefix || lpad((next_value - 1)::text, greatest(padding, length((next_value - 1)::text)), '0'))::text as number;

-- name: MarkInvoiceIssued :execrows
-- Step 4. Returns 0 rows if the invoice is no longer a draft.
update app.invoices
set status          = 'issued',
    sequence_value  = @sequence_value::bigint,
    number          = @number::text,
    issue_date      = @issue_date::date,
    issued_at       = now(),
    retention_until = @retention_until::date,
    tax_rounding    = @tax_rounding::text
where id = @id
  and status = 'draft';
