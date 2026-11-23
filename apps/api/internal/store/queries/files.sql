-- name: GetInvoiceFile :one
select * from app.invoice_files where invoice_id = @invoice_id and kind = @kind;

-- name: InsertInvoiceFile :execrows
-- A render that runs twice keeps the first row (ADR 0017, D2); the rows are
-- insert-only.
insert into app.invoice_files (tenant_id, invoice_id, kind, storage_path, content_type, byte_size, sha256)
values (app.current_tenant_id(), @invoice_id, @kind, @storage_path, @content_type, @byte_size, @sha256)
on conflict (invoice_id, kind) do nothing;

-- name: ListInvoiceFiles :many
select * from app.invoice_files where invoice_id = @invoice_id order by kind;
