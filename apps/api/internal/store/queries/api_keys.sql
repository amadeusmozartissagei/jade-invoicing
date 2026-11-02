-- FindAPIKey is hand-written in api_keys.go: sqlc cannot see the columns of a
-- function that returns a table.

-- name: TouchAPIKey :exec
-- At most one write per key per minute, so authentication does not write on
-- every request.
update app.api_keys
set last_used_at = now()
where id = @id
  and (last_used_at is null or last_used_at < now() - interval '1 minute');
