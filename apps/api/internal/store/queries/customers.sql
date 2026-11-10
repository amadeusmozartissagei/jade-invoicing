-- Customers (docs/design/data-model.md, "customers"). No delete: erasure
-- comes in phase 4.

-- name: CreateCustomer :one
insert into app.customers (
  tenant_id, external_ref, kind, name, trading_name,
  address_line1, address_line2, address_line3, city, post_code,
  country_subdivision, country_code, vat_id, legal_registration_id,
  established_in_de, default_buyer_reference,
  contact_name, contact_phone, contact_email, email
) values (
  app.current_tenant_id(), @external_ref, @kind, @name, @trading_name,
  @address_line1, @address_line2, @address_line3, @city, @post_code,
  @country_subdivision, @country_code, @vat_id, @legal_registration_id,
  @established_in_de, @default_buyer_reference,
  @contact_name, @contact_phone, @contact_email, @email
)
returning *;

-- name: GetCustomer :one
select * from app.customers where id = @id;

-- name: GetCustomerForUpdate :one
-- PATCH reads, merges in Go, and writes the whole row; the row lock keeps a
-- concurrent PATCH from merging into a stale copy.
select * from app.customers where id = @id for update;

-- name: UpdateCustomer :one
update app.customers set
  external_ref            = @external_ref,
  kind                    = @kind,
  name                    = @name,
  trading_name            = @trading_name,
  address_line1           = @address_line1,
  address_line2           = @address_line2,
  address_line3           = @address_line3,
  city                    = @city,
  post_code               = @post_code,
  country_subdivision     = @country_subdivision,
  country_code            = @country_code,
  vat_id                  = @vat_id,
  legal_registration_id   = @legal_registration_id,
  established_in_de       = @established_in_de,
  default_buyer_reference = @default_buyer_reference,
  contact_name            = @contact_name,
  contact_phone           = @contact_phone,
  contact_email           = @contact_email,
  email                   = @email
where id = @id
returning *;

-- name: ListCustomers :many
-- Keyset pagination on (created_at, id), newest first (ADR 0008, A6). The
-- row comparison matches the customers_list index. Ask for one row more
-- than the page to know whether there is a next page.
select * from app.customers
where (sqlc.narg(external_ref)::text is null or external_ref = sqlc.narg(external_ref))
  and (sqlc.narg(after_created_at)::timestamptz is null
       or (created_at, id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
order by created_at desc, id desc
limit @row_limit;
