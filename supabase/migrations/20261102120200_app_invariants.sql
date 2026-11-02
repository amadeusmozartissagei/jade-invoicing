-- Invariants a check constraint cannot express, enforced by triggers so they
-- hold even if the Go code has a bug (data-model.md, "Immutability, enforced
-- in the database"; ADR 0007).
--
-- Errors use SQLSTATEs in a class of our own, so Go and the tests can tell
-- them apart from other failures:
--   JD001  an issued, voided, or corrected document (or a child row of one)
--          was changed, or an insert-only row was updated or deleted
--   JD002  a payment for an invoice that cannot be paid
--   JD003  an amount with more decimals than the invoice's currency allows

-- ---------- updated_at ----------

create trigger tenants_updated_at before update on app.tenants
  for each row execute function app.set_updated_at();
create trigger seller_profiles_updated_at before update on app.seller_profiles
  for each row execute function app.set_updated_at();
-- Recording use (last_used_at, about once a minute) is not a change to the key.
create trigger api_keys_updated_at before update on app.api_keys
  for each row
  when ((to_jsonb(old) - 'last_used_at' - 'updated_at') is distinct from (to_jsonb(new) - 'last_used_at' - 'updated_at'))
  execute function app.set_updated_at();
create trigger customers_updated_at before update on app.customers
  for each row execute function app.set_updated_at();
create trigger number_series_updated_at before update on app.number_series
  for each row execute function app.set_updated_at();
create trigger invoices_updated_at before update on app.invoices
  for each row execute function app.set_updated_at();
create trigger webhook_endpoints_updated_at before update on app.webhook_endpoints
  for each row execute function app.set_updated_at();
create trigger webhook_deliveries_updated_at before update on app.webhook_deliveries
  for each row execute function app.set_updated_at();

-- ---------- API keys and number series ----------

-- Revocation is final, and a key's identity never changes.
create function app.guard_api_key()
returns trigger
language plpgsql
set search_path = ''
as $$
begin
  if new.id <> old.id or new.tenant_id <> old.tenant_id or new.prefix <> old.prefix
     or new.secret_hash <> old.secret_hash or new.created_at <> old.created_at then
    raise exception 'api key %: id, tenant, prefix, and secret hash never change', old.id
      using errcode = 'JD001';
  end if;
  if old.revoked_at is not null and new.revoked_at is distinct from old.revoked_at then
    raise exception 'api key % is revoked; revocation cannot be undone or moved', old.id
      using errcode = 'JD001';
  end if;
  return new;
end
$$;

create trigger api_keys_guard before update on app.api_keys
  for each row execute function app.guard_api_key();

-- A series only counts up by one per issue (D6): a jump would leave a gap in
-- the invoice numbers, and its code, prefix, and padding never change once
-- created, so issued numbers keep one format.
create function app.guard_number_series()
returns trigger
language plpgsql
set search_path = ''
as $$
begin
  if new.id <> old.id or new.tenant_id <> old.tenant_id or new.code <> old.code
     or new.prefix <> old.prefix or new.padding <> old.padding or new.created_at <> old.created_at then
    raise exception 'number series %: only next_value can change', old.id
      using errcode = 'JD001';
  end if;
  if new.next_value <> old.next_value + 1 and new.next_value <> old.next_value then
    raise exception 'number series %: next_value can only go from % to %', old.id, old.next_value, old.next_value + 1
      using errcode = 'JD001';
  end if;
  return new;
end
$$;

create trigger number_series_guard before update on app.number_series
  for each row execute function app.guard_number_series();

-- ---------- every tenant gets a default number series ----------

create function app.create_default_series()
returns trigger
language plpgsql
set search_path = ''
as $$
begin
  insert into app.number_series (tenant_id, code) values (new.id, 'default');
  return null;
end
$$;

create trigger tenants_default_series after insert on app.tenants
  for each row execute function app.create_default_series();

-- ---------- invoices ----------

-- A draft may change freely but only become issued. An issued document may
-- only become voided or corrected, changing nothing but those columns. A
-- voided or corrected document cannot change. Only drafts can be deleted.
create function app.guard_invoice()
returns trigger
language plpgsql
set search_path = ''
as $$
declare
  -- Columns an issued document may change on its way to voided or corrected.
  transition_columns constant text[] := array['status', 'voided_at', 'void_reason', 'corrected_at', 'updated_at'];
begin
  if tg_op = 'DELETE' then
    if old.status <> 'draft' then
      raise exception 'invoice % is %; only drafts can be deleted', old.id, old.status
        using errcode = 'JD001';
    end if;
    return old;
  end if;

  if new.id <> old.id or new.tenant_id <> old.tenant_id or new.document_type <> old.document_type
     or new.preceding_invoice_id is distinct from old.preceding_invoice_id
     or new.jurisdiction <> old.jurisdiction or new.currency <> old.currency
     or new.created_at <> old.created_at then
    raise exception 'invoice %: id, tenant, document type, preceding invoice, jurisdiction, currency, and created_at never change', old.id
      using errcode = 'JD001';
  end if;

  case old.status
    when 'draft' then
      if new.status not in ('draft', 'issued') then
        raise exception 'invoice % is a draft; it can only be issued, not %', old.id, new.status
          using errcode = 'JD001';
      end if;
    when 'issued' then
      if new.status not in ('voided', 'corrected') then
        raise exception 'invoice % is issued; it can only become voided or corrected', old.id
          using errcode = 'JD001';
      end if;
      if (pg_catalog.to_jsonb(new) - transition_columns) <> (pg_catalog.to_jsonb(old) - transition_columns) then
        raise exception 'invoice % is issued; only its status can change', old.id
          using errcode = 'JD001';
      end if;
    else
      raise exception 'invoice % is %; it cannot change', old.id, old.status
        using errcode = 'JD001';
  end case;
  return new;
end
$$;

create trigger invoices_guard before update or delete on app.invoices
  for each row execute function app.guard_invoice();

-- Lines, tax breakdowns, and parties change only while their invoice is a
-- draft. The parent is read FOR SHARE, so a change waits for a concurrent
-- issue to commit and then sees the new status. A missing parent means a
-- draft is being deleted with its children (ON DELETE CASCADE).
create function app.guard_invoice_child()
returns trigger
language plpgsql
set search_path = ''
as $$
declare
  row_data jsonb := pg_catalog.to_jsonb(case when tg_op = 'DELETE' then old else new end);
  invoice_status text;
  invoice_currency text;
begin
  if tg_op = 'UPDATE' and (new.invoice_id <> old.invoice_id or new.tenant_id <> old.tenant_id) then
    raise exception '%: a row cannot move to another invoice', tg_table_name
      using errcode = 'JD001';
  end if;

  select i.status, i.currency into invoice_status, invoice_currency
    from app.invoices i
    where i.id = (row_data ->> 'invoice_id')::uuid
    for share;

  if not found then
    -- Deleted by cascade from its draft, or an insert the foreign key rejects.
    return case when tg_op = 'DELETE' then old else new end;
  end if;

  if invoice_status <> 'draft' then
    raise exception '%: invoice % is %; its contents cannot change', tg_table_name, row_data ->> 'invoice_id', invoice_status
      using errcode = 'JD001';
  end if;

  if tg_op <> 'DELETE' and not (
    app.fits_currency((row_data ->> 'line_net_amount')::numeric, invoice_currency)
    and app.fits_currency((row_data ->> 'taxable_amount')::numeric, invoice_currency)
    and app.fits_currency((row_data ->> 'tax_amount')::numeric, invoice_currency)
  ) then
    raise exception '%: an amount has more decimals than % allows', tg_table_name, invoice_currency
      using errcode = 'JD003';
  end if;

  return case when tg_op = 'DELETE' then old else new end;
end
$$;

create trigger invoice_lines_guard before insert or update or delete on app.invoice_lines
  for each row execute function app.guard_invoice_child();
create trigger invoice_tax_breakdowns_guard before insert or update or delete on app.invoice_tax_breakdowns
  for each row execute function app.guard_invoice_child();
create trigger invoice_parties_guard before insert or update or delete on app.invoice_parties
  for each row execute function app.guard_invoice_child();

-- ---------- payments ----------

-- Only an issued invoice or corrected invoice can be paid; a credit note is
-- not paid by the buyer (D9).
create function app.guard_payment()
returns trigger
language plpgsql
set search_path = ''
as $$
declare
  invoice_status text;
  invoice_type text;
  invoice_currency text;
begin
  select i.status, i.document_type, i.currency into invoice_status, invoice_type, invoice_currency
    from app.invoices i
    where i.id = new.invoice_id
    for share;

  if not found then
    return new; -- the foreign key rejects it
  end if;

  if invoice_status <> 'issued' or invoice_type = 'credit_note' then
    raise exception 'invoice % is a % %; only an issued invoice can be paid', new.invoice_id, invoice_status, invoice_type
      using errcode = 'JD002';
  end if;

  if not app.fits_currency(new.amount, invoice_currency) then
    raise exception 'payment amount % has more decimals than % allows', new.amount, invoice_currency
      using errcode = 'JD003';
  end if;

  return new;
end
$$;

create trigger payments_guard before insert on app.payments
  for each row execute function app.guard_payment();

-- ---------- insert-only tables ----------

-- jade_api has no UPDATE or DELETE grant on these; the trigger stops every
-- other role as well.
create function app.reject_change()
returns trigger
language plpgsql
set search_path = ''
as $$
begin
  raise exception '% is insert-only; % is not allowed', tg_table_name, tg_op
    using errcode = 'JD001';
end
$$;

create trigger invoice_files_insert_only before update or delete or truncate on app.invoice_files
  for each statement execute function app.reject_change();
create trigger payments_insert_only before update or delete or truncate on app.payments
  for each statement execute function app.reject_change();
create trigger events_insert_only before update or delete or truncate on app.events
  for each statement execute function app.reject_change();
create trigger webhook_attempts_insert_only before update or delete or truncate on app.webhook_attempts
  for each statement execute function app.reject_change();
create trigger audit_log_insert_only before update or delete or truncate on app.audit_log
  for each statement execute function app.reject_change();

-- Trigger functions need no EXECUTE grant to fire.
revoke execute on all functions in schema app from public;
