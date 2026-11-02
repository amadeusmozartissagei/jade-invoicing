-- name: GetCurrentTenant :one
select * from app.tenants where id = app.current_tenant_id();
