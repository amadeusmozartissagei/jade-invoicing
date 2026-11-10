-- Idempotency keys (docs/design/api.md, "Idempotency"; ADR 0014). Every query
-- runs in the request's tenant transaction, so RLS scopes the key to the
-- tenant.

-- name: TryIdempotencyLock :one
-- Held until the transaction ends, so it is safe behind the transaction
-- pooler. false: another request with this key is running right now.
select pg_try_advisory_xact_lock(
  hashtextextended(app.current_tenant_id()::text || ':' || @key::text, 0)
)::boolean as locked;

-- name: GetIdempotencyKey :one
-- expired uses the transaction's now(), the same clock the insert uses.
select request_hash, response_status, response_headers, response_body,
       (expires_at <= now())::boolean as expired
from app.idempotency_keys
where key = @key;

-- name: DeleteIdempotencyKey :exec
delete from app.idempotency_keys where key = @key;

-- name: InsertIdempotencyKey :exec
insert into app.idempotency_keys (
  tenant_id, key, method, path, request_hash,
  response_status, response_headers, response_body, expires_at
) values (
  app.current_tenant_id(), @key, @method, @path, @request_hash,
  @response_status, (@response_headers::text)::jsonb, @response_body, now() + interval '24 hours'
);
