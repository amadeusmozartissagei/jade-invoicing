-- The app schema, the API's login role, and helper functions
-- (docs/design/data-model.md, ADR 0007 D2).
--
-- jade_api is created without a password, so it cannot log in until one is
-- set outside the migrations (apps/api/README.md). Tests use `set role
-- jade_api` from postgres instead, which is why postgres is granted SET on it.
-- Checked on 2026-11-02: a custom login role connects through the Supabase
-- transaction pooler as `<role>.<project_ref>`.

do $$
begin
  if not exists (select from pg_catalog.pg_roles where rolname = 'jade_api') then
    create role jade_api login nobypassrls noinherit;
  end if;
end
$$;

grant jade_api to postgres with inherit false, set true;

create schema app;

-- Supabase applies no default privileges to new schemas (checked on the
-- project and locally, 2026-11-02), but Postgres lets PUBLIC execute every new
-- function, and a per-schema ALTER DEFAULT PRIVILEGES cannot take that away
-- (it only adds to the global defaults). So every migration that creates
-- functions in app ends with REVOKE EXECUTE ... FROM PUBLIC, and a test checks
-- that anon and authenticated have no rights in app (criterion 9).
revoke all on schema app from public;

grant usage on schema app to jade_api;

-- The tenant of the current transaction, set by the API with
-- set_config('app.tenant_id', $1, true). Null when unset, so RLS policies
-- match no rows (fail closed).
create function app.current_tenant_id()
returns uuid
language sql
stable
set search_path = ''
as $$
  select nullif(pg_catalog.current_setting('app.tenant_id', true), '')::uuid
$$;

-- Decimal places of a currency's minor unit. Every currency the platform
-- accepts must be listed; an unknown one returns null, and the checks that use
-- it then fail the row.
create function app.minor_units(currency text)
returns integer
language sql
immutable
strict
set search_path = ''
as $$
  select case currency when 'EUR' then 2 when 'JPY' then 0 end
$$;

-- True when amount has at most 16 integer digits (the contract's limit) and no
-- more decimals than the currency allows (D1). Null passes, as in a check.
create function app.fits_currency(amount numeric, currency text)
returns boolean
language sql
immutable
set search_path = ''
as $$
  select amount is null
    or coalesce(pg_catalog.abs(amount) < 1e16 and amount = pg_catalog.round(amount, app.minor_units(currency)), false)
$$;

grant execute on function app.current_tenant_id() to jade_api;
grant execute on function app.minor_units(text) to jade_api;
grant execute on function app.fits_currency(numeric, text) to jade_api;

create function app.set_updated_at()
returns trigger
language plpgsql
set search_path = ''
as $$
begin
  new.updated_at := pg_catalog.now();
  return new;
end
$$;

revoke execute on all functions in schema app from public;
