-- Row-level security, jade_api's rights, the API key lookup, and the webhook
-- queue (data-model.md, "Roles and row-level security"; ADR 0007 D2).
--
-- RLS is enabled and FORCED on every table, so it applies to the table owner
-- too (postgres still bypasses it through its BYPASSRLS attribute). Each
-- tenant table has the same one policy. current_tenant_id() is wrapped in a
-- scalar subquery so Postgres evaluates it once per statement, not per row.

-- ---------- tenant isolation ----------

alter table app.tenants enable row level security;
alter table app.tenants force row level security;
create policy tenant_isolation on app.tenants
  using (id = (select app.current_tenant_id()))
  with check (id = (select app.current_tenant_id()));

do $$
declare
  t text;
begin
  foreach t in array array[
    'seller_profiles', 'api_keys', 'customers', 'number_series', 'invoices', 'invoice_lines',
    'invoice_tax_breakdowns', 'invoice_parties', 'invoice_files', 'payments', 'idempotency_keys',
    'events', 'webhook_endpoints', 'webhook_deliveries', 'webhook_attempts', 'audit_log'
  ] loop
    execute format('alter table app.%I enable row level security', t);
    execute format('alter table app.%I force row level security', t);
    execute format(
      'create policy tenant_isolation on app.%I
         using (tenant_id = (select app.current_tenant_id()))
         with check (tenant_id = (select app.current_tenant_id()))', t);
  end loop;
end
$$;

-- Reference data: everyone may read, nobody may write (no write grants).
alter table app.tax_rates enable row level security;
alter table app.tax_rates force row level security;
create policy read_all on app.tax_rates for select using (true);

-- ---------- jade_api's table rights (S, I, U, D per data-model.md) ----------

-- A tenant may rename itself and change its settings, but not its
-- jurisdiction, currency, or status.
grant select on app.tenants to jade_api;
grant update (name, timezone, jp_tax_rounding, fiscal_year_end_month) on app.tenants to jade_api;

grant select, insert, update on app.seller_profiles, app.customers, app.webhook_endpoints,
  app.webhook_deliveries to jade_api;

-- Keys are created and revoked, never edited (the contract has no other
-- operation), and a series only counts. Narrow column rights, plus the
-- guard triggers, keep a bug from replacing a key's hash or skipping numbers.
grant select, insert on app.api_keys, app.number_series to jade_api;
grant update (revoked_at, last_used_at) on app.api_keys to jade_api;
grant update (next_value) on app.number_series to jade_api;
grant select on app.tax_rates to jade_api;
grant select, insert, update, delete on app.invoices, app.invoice_lines, app.invoice_tax_breakdowns,
  app.idempotency_keys to jade_api;
grant select, insert on app.invoice_parties, app.invoice_files, app.payments, app.events,
  app.webhook_attempts, app.audit_log to jade_api;

-- ---------- API key lookup ----------

-- Authentication runs before the tenant is known, so RLS would hide every
-- key. This function returns the one key with the given prefix, across
-- tenants, and nothing else. Go compares the hash in constant time and
-- rejects expired or revoked keys.
create function app.find_api_key(key_prefix text)
returns table (
  id uuid,
  tenant_id uuid,
  secret_hash bytea,
  expires_at timestamptz,
  revoked_at timestamptz
)
language sql
stable
security definer
set search_path = ''
as $$
  select k.id, k.tenant_id, k.secret_hash, k.expires_at, k.revoked_at
  from app.api_keys k
  where k.prefix = key_prefix
$$;

revoke all on function app.find_api_key(text) from public;
grant execute on function app.find_api_key(text) to jade_api;

-- ---------- webhook delivery queue (ADR 0003, ADR 0009) ----------

-- Messages carry only IDs (delivery_id, tenant_id): pgmq's tables have no RLS.
-- pgmq's functions run with the caller's rights, so jade_api needs rights on
-- this queue's two tables (checked 2026-11-02 with pgmq 1.5.1): send, read,
-- set_vt, and delete use q_; archive moves a row from q_ to a_ with
-- INSERT ... RETURNING, which needs SELECT too.
select pgmq.create('webhook_deliveries');

grant usage on schema pgmq to jade_api;
grant select, insert, update, delete on pgmq.q_webhook_deliveries to jade_api;
grant select, insert on pgmq.a_webhook_deliveries to jade_api;

revoke execute on all functions in schema app from public;
