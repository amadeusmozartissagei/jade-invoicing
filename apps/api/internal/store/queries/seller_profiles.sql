-- name: GetSellerProfile :one
select * from app.seller_profiles where tenant_id = app.current_tenant_id();

-- name: PutSellerProfile :one
-- Replaces every field (PUT): a field the client omits becomes null. The
-- admin script creates the row with the tenant, so the insert branch only
-- runs if that ever failed; RLS checks the tenant either way.
insert into app.seller_profiles (
  tenant_id, legal_name, trading_name, address_line1, address_line2, address_line3,
  city, post_code, country_subdivision, country_code, vat_id, tax_number,
  jp_registration_number, contact_name, contact_phone, contact_email, iban, bic, account_name
) values (
  app.current_tenant_id(), @legal_name, @trading_name, @address_line1, @address_line2, @address_line3,
  @city, @post_code, @country_subdivision, @country_code, @vat_id, @tax_number,
  @jp_registration_number, @contact_name, @contact_phone, @contact_email, @iban, @bic, @account_name
)
on conflict (tenant_id) do update set
  legal_name             = excluded.legal_name,
  trading_name           = excluded.trading_name,
  address_line1          = excluded.address_line1,
  address_line2          = excluded.address_line2,
  address_line3          = excluded.address_line3,
  city                   = excluded.city,
  post_code              = excluded.post_code,
  country_subdivision    = excluded.country_subdivision,
  country_code           = excluded.country_code,
  vat_id                 = excluded.vat_id,
  tax_number             = excluded.tax_number,
  jp_registration_number = excluded.jp_registration_number,
  contact_name           = excluded.contact_name,
  contact_phone          = excluded.contact_phone,
  contact_email          = excluded.contact_email,
  iban                   = excluded.iban,
  bic                    = excluded.bic,
  account_name           = excluded.account_name
returning *;
