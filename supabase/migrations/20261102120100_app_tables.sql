-- Phase 1 tables (docs/design/data-model.md, ADR 0007).
--
-- Every tenant table carries tenant_id, and references between tenant tables
-- are composite foreign keys on (tenant_id, id), so a row can never point at
-- another tenant's row. Codes are text with checks. Triggers are in the next
-- migration, RLS and grants in the one after.
--
-- Money (D1) is plain numeric, checked by app.fits_currency, not
-- numeric(18,2): a column with a scale would silently round 0.125 to 0.13
-- before any check could see it, and an amount with too many decimals must be
-- rejected instead. Quantities and prices keep numeric(18,6), the contract's
-- limit.

-- ---------- tenants and their settings ----------

create table app.tenants (
  id                    uuid primary key default gen_random_uuid(),
  name                  text not null check (name <> ''),
  jurisdiction          text not null check (jurisdiction in ('DE', 'JP')),
  currency              text not null,
  timezone              text not null,
  jp_tax_rounding       text check (jp_tax_rounding in ('down', 'up', 'half_up')),
  fiscal_year_end_month smallint not null check (fiscal_year_end_month between 1 and 12),
  status                text not null default 'active' check (status in ('active', 'suspended')),
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),
  constraint tenants_currency_matches_jurisdiction check (
    (jurisdiction = 'DE' and currency = 'EUR') or (jurisdiction = 'JP' and currency = 'JPY')
  ),
  -- The issuer chooses the rounding of Japanese consumption tax [NTA-QA-57].
  constraint tenants_jp_tax_rounding_only_for_jp check (
    (jurisdiction = 'JP') = (jp_tax_rounding is not null)
  )
);

create table app.seller_profiles (
  tenant_id              uuid primary key references app.tenants (id),
  legal_name             text,
  trading_name           text,
  address_line1          text,
  address_line2          text,
  address_line3          text,
  city                   text,
  post_code              text,
  country_subdivision    text,
  country_code           text check (country_code ~ '^[A-Z]{2}$'),
  vat_id                 text,
  tax_number             text,
  jp_registration_number text check (jp_registration_number ~ '^T[0-9]{13}$'),
  contact_name           text,
  contact_phone          text,
  contact_email          text,
  iban                   text,
  bic                    text,
  account_name           text,
  created_at             timestamptz not null default now(),
  updated_at             timestamptz not null default now()
);

create table app.api_keys (
  id           uuid primary key default gen_random_uuid(),
  tenant_id    uuid not null references app.tenants (id),
  name         text not null check (name <> ''),
  prefix       text not null unique,
  secret_hash  bytea not null check (octet_length(secret_hash) = 32),
  expires_at   timestamptz,
  revoked_at   timestamptz,
  last_used_at timestamptz,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

create index api_keys_list on app.api_keys (tenant_id, created_at desc, id desc);

create table app.customers (
  id                      uuid primary key default gen_random_uuid(),
  tenant_id               uuid not null references app.tenants (id),
  external_ref            text,
  kind                    text not null check (kind in ('business', 'individual')),
  name                    text not null check (name <> ''),
  trading_name            text,
  address_line1           text,
  address_line2           text,
  address_line3           text,
  city                    text,
  post_code               text,
  country_subdivision     text,
  country_code            text check (country_code ~ '^[A-Z]{2}$'),
  vat_id                  text,
  legal_registration_id   text,
  established_in_de       boolean,
  default_buyer_reference text,
  contact_name            text,
  contact_phone           text,
  contact_email           text,
  email                   text,
  created_at              timestamptz not null default now(),
  updated_at              timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, external_ref)
);

create index customers_list on app.customers (tenant_id, created_at desc, id desc);

create table app.number_series (
  id         uuid primary key default gen_random_uuid(),
  tenant_id  uuid not null references app.tenants (id),
  code       text not null check (code ~ '^[a-z0-9_-]{1,32}$'),
  prefix     text not null default '' check (length(prefix) <= 40),
  padding    smallint not null default 1 check (padding between 1 and 12),
  next_value bigint not null default 1 check (next_value >= 1),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, code)
);

-- Global reference data, written only by migrations. A rate applies to lines
-- whose supply date falls in [valid_from, valid_to].
create table app.tax_rates (
  jurisdiction text not null check (jurisdiction in ('DE', 'JP')),
  tax_category text not null check (tax_category in ('S')),
  rate         numeric(5,2) not null check (rate between 0 and 100),
  valid_from   date not null,
  valid_to     date,
  primary key (jurisdiction, tax_category, rate, valid_from),
  check (valid_to is null or valid_to >= valid_from)
);

-- valid_from is the earliest supply date this platform accepts for the rate,
-- not the date the law introduced it: for JP the start of the Qualified
-- Invoice System, for DE the start of the E-Rechnung transition (both in
-- docs/domain/). Earlier supply dates are out of scope.
insert into app.tax_rates (jurisdiction, tax_category, rate, valid_from) values
  ('DE', 'S', 19.00, '2025-01-01'), -- [UStG-12]
  ('DE', 'S',  7.00, '2025-01-01'), -- [UStG-12]
  ('JP', 'S', 10.00, '2023-10-01'), -- [NTA-EN]
  ('JP', 'S',  8.00, '2023-10-01'); -- [NTA-EN], reduced rate (軽減税率)

-- ---------- invoices, credit notes, corrected invoices ----------

create table app.invoices (
  id                   uuid primary key default gen_random_uuid(),
  tenant_id            uuid not null references app.tenants (id),
  document_type        text not null check (document_type in ('invoice', 'credit_note', 'corrected_invoice')),
  status               text not null default 'draft' check (status in ('draft', 'issued', 'voided', 'corrected')),
  customer_id          uuid not null,
  series_id            uuid not null,
  preceding_invoice_id uuid,
  sequence_value       bigint check (sequence_value >= 1),
  number               text,
  jurisdiction         text not null check (jurisdiction in ('DE', 'JP')),
  currency             text not null check (currency in ('EUR', 'JPY')),
  issue_date           date,
  supply_date          date,
  supply_period_start  date,
  supply_period_end    date,
  due_date             date,
  buyer_reference      text,
  order_reference      text,
  note                 text,
  payment_terms        text,
  tax_rounding         text check (tax_rounding in ('half_away_from_zero', 'down', 'up', 'half_up')),
  line_net_total       numeric       not null default 0,
  allowance_total      numeric       not null default 0 check (allowance_total = 0), -- D7
  charge_total         numeric       not null default 0 check (charge_total = 0),       -- D7
  tax_exclusive_total  numeric       not null default 0,
  tax_total            numeric       not null default 0,
  tax_inclusive_total  numeric       not null default 0,
  prepaid_amount       numeric       not null default 0 check (prepaid_amount >= 0),
  rounding_amount      numeric       not null default 0,
  amount_due           numeric       not null default 0,
  version              integer not null default 1 check (version >= 1),
  retention_until      date,
  issued_at            timestamptz,
  voided_at            timestamptz,
  void_reason          text,
  corrected_at         timestamptz,
  created_at           timestamptz not null default now(),
  updated_at           timestamptz not null default now(),

  unique (tenant_id, id),
  foreign key (tenant_id, customer_id) references app.customers (tenant_id, id),
  foreign key (tenant_id, series_id) references app.number_series (tenant_id, id),
  foreign key (tenant_id, preceding_invoice_id) references app.invoices (tenant_id, id),
  constraint invoices_number_unique unique (tenant_id, number),
  constraint invoices_sequence_unique unique (series_id, sequence_value),

  constraint invoices_currency_matches_jurisdiction check (
    (jurisdiction = 'DE' and currency = 'EUR') or (jurisdiction = 'JP' and currency = 'JPY')
  ),
  -- A draft has no number and no legal effect; issuing sets all of these at once.
  constraint invoices_issue_fields check (
    case when status = 'draft'
      then num_nonnulls(number, sequence_value, issue_date, issued_at, retention_until, tax_rounding) = 0
      else num_nulls(number, sequence_value, issue_date, issued_at, retention_until, tax_rounding) = 0
    end
  ),
  constraint invoices_voided_at check ((status = 'voided') = (voided_at is not null)),
  constraint invoices_void_reason check (void_reason is null or status = 'voided'),
  constraint invoices_corrected_at check ((status = 'corrected') = (corrected_at is not null)),
  constraint invoices_only_invoices_end_voided_or_corrected check (
    status not in ('voided', 'corrected') or document_type in ('invoice', 'corrected_invoice')
  ),
  constraint invoices_preceding_invoice check (
    (document_type = 'invoice') = (preceding_invoice_id is null) and preceding_invoice_id is distinct from id
  ),
  constraint invoices_supply_date check (
    status = 'draft' or supply_date is not null or supply_period_start is not null
  ),
  constraint invoices_supply_period check (supply_period_end >= supply_period_start),
  constraint invoices_tax_rounding_matches_jurisdiction check (
    tax_rounding is null
    or (jurisdiction = 'DE' and tax_rounding = 'half_away_from_zero')
    or (jurisdiction = 'JP' and tax_rounding in ('down', 'up', 'half_up'))
  ),
  -- EN 16931 BR-CO-13, BR-CO-15, BR-CO-16.
  constraint invoices_br_co_totals check (
    tax_exclusive_total = line_net_total - allowance_total + charge_total
    and tax_inclusive_total = tax_exclusive_total + tax_total
    and amount_due = tax_inclusive_total - prepaid_amount + rounding_amount
  ),
  constraint invoices_amounts_fit_currency check (
    app.fits_currency(line_net_total, currency)
    and app.fits_currency(tax_exclusive_total, currency)
    and app.fits_currency(tax_total, currency)
    and app.fits_currency(tax_inclusive_total, currency)
    and app.fits_currency(prepaid_amount, currency)
    and app.fits_currency(rounding_amount, currency)
    and app.fits_currency(amount_due, currency)
  )
);

-- At most one issued correction per original (D10).
create unique index invoices_one_issued_correction on app.invoices (preceding_invoice_id)
  where document_type = 'corrected_invoice' and status <> 'draft';

create index invoices_list_by_status on app.invoices (tenant_id, status, created_at desc, id desc);
create index invoices_list_by_customer on app.invoices (tenant_id, customer_id, created_at desc, id desc);
create index invoices_by_preceding on app.invoices (tenant_id, preceding_invoice_id)
  where preceding_invoice_id is not null;

create table app.invoice_lines (
  tenant_id           uuid not null,
  invoice_id          uuid not null,
  position            smallint not null check (position between 1 and 500),
  name                text not null check (name <> ''),
  description         text,
  quantity            numeric(18,6) not null check (quantity > 0),
  unit_code           text not null default 'C62' check (unit_code ~ '^[A-Z0-9]{1,3}$'),
  unit_price          numeric(18,6) not null check (unit_price >= 0),
  price_base_quantity numeric(18,6) not null default 1 check (price_base_quantity > 0),
  tax_category        text not null default 'S' check (tax_category in ('S')),
  tax_rate            numeric(5,2) not null check (tax_rate between 0 and 100),
  line_net_amount     numeric       not null check (line_net_amount >= 0),
  period_start        date,
  period_end          date,
  primary key (invoice_id, position),
  foreign key (tenant_id, invoice_id) references app.invoices (tenant_id, id) on delete cascade,
  check (period_end >= period_start)
);

create table app.invoice_tax_breakdowns (
  tenant_id      uuid not null,
  invoice_id     uuid not null,
  tax_category   text not null check (tax_category in ('S')),
  tax_rate       numeric(5,2) not null check (tax_rate between 0 and 100),
  taxable_amount numeric       not null,
  tax_amount     numeric       not null,
  primary key (invoice_id, tax_category, tax_rate),
  foreign key (tenant_id, invoice_id) references app.invoices (tenant_id, id) on delete cascade
);

-- The seller's and buyer's details as they were at issue (D5).
create table app.invoice_parties (
  tenant_id              uuid not null,
  invoice_id             uuid not null,
  role                   text not null check (role in ('seller', 'buyer')),
  name                   text,
  trading_name           text,
  address_line1          text,
  address_line2          text,
  address_line3          text,
  city                   text,
  post_code              text,
  country_subdivision    text,
  country_code           text check (country_code ~ '^[A-Z]{2}$'),
  vat_id                 text,
  tax_number             text,
  legal_registration_id  text,
  jp_registration_number text check (jp_registration_number ~ '^T[0-9]{13}$'),
  contact_name           text,
  contact_phone          text,
  contact_email          text,
  iban                   text,
  bic                    text,
  account_name           text,
  primary key (invoice_id, role),
  foreign key (tenant_id, invoice_id) references app.invoices (tenant_id, id) on delete cascade
);

create table app.invoice_files (
  id           uuid primary key default gen_random_uuid(),
  tenant_id    uuid not null,
  invoice_id   uuid not null,
  kind         text not null check (kind in ('pdf', 'xrechnung', 'zugferd', 'validation_report')),
  storage_path text not null,
  content_type text not null,
  byte_size    bigint not null check (byte_size >= 0),
  sha256       bytea not null check (octet_length(sha256) = 32),
  created_at   timestamptz not null default now(),
  foreign key (tenant_id, invoice_id) references app.invoices (tenant_id, id),
  unique (invoice_id, kind)
);

-- Insert-only; an invoice is paid when its payments reach amount_due (D9).
create table app.payments (
  id           uuid primary key default gen_random_uuid(),
  tenant_id    uuid not null,
  invoice_id   uuid not null,
  amount       numeric       not null check (amount > 0),
  paid_on      date not null,
  method       text,
  external_ref text,
  created_at   timestamptz not null default now(),
  foreign key (tenant_id, invoice_id) references app.invoices (tenant_id, id)
);

create index payments_by_invoice on app.payments (tenant_id, invoice_id);

-- ---------- idempotency (ADR 0008, A3) ----------

create table app.idempotency_keys (
  tenant_id       uuid not null references app.tenants (id),
  key             text not null check (length(key) between 1 and 255),
  method          text not null,
  path            text not null,
  request_hash    bytea not null check (octet_length(request_hash) = 32),
  response_status smallint not null check (response_status between 200 and 299),
  response_body   bytea not null,
  created_at      timestamptz not null default now(),
  expires_at      timestamptz not null,
  primary key (tenant_id, key)
);

create index idempotency_keys_expiry on app.idempotency_keys (expires_at);

-- ---------- events and webhooks (ADR 0009) ----------

-- The transactional outbox. payload never holds personal data (W2).
create table app.events (
  id           uuid primary key default gen_random_uuid(),
  tenant_id    uuid not null references app.tenants (id),
  type         text not null check (type in (
                 'invoice.issued', 'invoice.voided', 'invoice.corrected', 'invoice.paid', 'credit_note.issued')),
  subject_type text not null check (subject_type in ('invoice')),
  subject_id   uuid not null,
  payload      jsonb not null,
  created_at   timestamptz not null default now(),
  unique (tenant_id, id)
);

create index events_by_subject on app.events (tenant_id, subject_type, subject_id);

create table app.webhook_endpoints (
  id                         uuid primary key default gen_random_uuid(),
  tenant_id                  uuid not null references app.tenants (id),
  url                        text not null check (url ~ '^https://'),
  secret_ciphertext          bytea not null,
  secret_key_id              text not null,
  previous_secret_ciphertext bytea,
  previous_secret_key_id     text,
  previous_secret_expires_at timestamptz,
  event_types                text[] not null default '{}' check (event_types <@ array[
                               'invoice.issued', 'invoice.voided', 'invoice.corrected', 'invoice.paid',
                               'credit_note.issued']),
  enabled                    boolean not null default true,
  disabled_reason            text,
  created_at                 timestamptz not null default now(),
  updated_at                 timestamptz not null default now(),
  unique (tenant_id, id),
  -- The previous secret exists only during the 24 hours after a rotation (W4).
  constraint webhook_endpoints_previous_secret check (
    num_nulls(previous_secret_ciphertext, previous_secret_key_id, previous_secret_expires_at) in (0, 3)
  )
);

create index webhook_endpoints_list on app.webhook_endpoints (tenant_id, created_at desc, id desc);

create table app.webhook_deliveries (
  id                   uuid primary key default gen_random_uuid(),
  tenant_id            uuid not null,
  event_id             uuid not null,
  endpoint_id          uuid not null,
  status               text not null default 'pending' check (status in ('pending', 'succeeded', 'dead')),
  attempt_count        integer not null default 0 check (attempt_count >= 0),
  next_attempt_at      timestamptz,
  last_attempt_at      timestamptz,
  last_response_status smallint,
  last_error           text,
  succeeded_at         timestamptz,
  created_at           timestamptz not null default now(),
  updated_at           timestamptz not null default now(),
  unique (tenant_id, id),
  unique (event_id, endpoint_id),
  foreign key (tenant_id, event_id) references app.events (tenant_id, id),
  foreign key (tenant_id, endpoint_id) references app.webhook_endpoints (tenant_id, id),
  check ((status = 'succeeded') = (succeeded_at is not null))
);

create index webhook_deliveries_due on app.webhook_deliveries (tenant_id, status, next_attempt_at);
create index webhook_deliveries_list on app.webhook_deliveries (tenant_id, endpoint_id, created_at desc, id desc);

create table app.webhook_attempts (
  id              bigint generated always as identity primary key,
  tenant_id       uuid not null,
  delivery_id     uuid not null,
  attempt_no      integer not null check (attempt_no >= 1),
  started_at      timestamptz not null,
  duration_ms     integer check (duration_ms >= 0),
  response_status smallint,
  error           text check (length(error) <= 1000),
  created_at      timestamptz not null default now(),
  foreign key (tenant_id, delivery_id) references app.webhook_deliveries (tenant_id, id),
  unique (delivery_id, attempt_no)
);

-- ---------- audit log ----------

-- details never holds personal data, so erasure never edits this table.
create table app.audit_log (
  id           bigint generated always as identity primary key,
  tenant_id    uuid not null references app.tenants (id),
  occurred_at  timestamptz not null default now(),
  actor_type   text not null check (actor_type in ('api_key', 'user', 'system')),
  actor_id     uuid,
  action       text not null,
  subject_type text,
  subject_id   uuid,
  request_id   text,
  details      jsonb not null default '{}'
);

create index audit_log_by_time on app.audit_log (tenant_id, occurred_at desc);
create index audit_log_by_subject on app.audit_log (tenant_id, subject_type, subject_id);
