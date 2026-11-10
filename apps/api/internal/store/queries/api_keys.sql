-- FindAPIKey is hand-written in api_keys.go: sqlc cannot see the columns of a
-- function that returns a table.

-- name: TouchAPIKey :exec
-- At most one write per key per minute, so authentication does not write on
-- every request. SKIP LOCKED: if another request of the same key holds the
-- row, this one skips the touch instead of waiting for that request to end;
-- otherwise concurrent requests of one key would run one after another, and a
-- repeated Idempotency-Key would wait instead of getting 409 (ADR 0014).
update app.api_keys
set last_used_at = now()
where id = (
  select k.id from app.api_keys k
  where k.id = @id
    and (k.last_used_at is null or k.last_used_at < now() - interval '1 minute')
  for update skip locked
);
