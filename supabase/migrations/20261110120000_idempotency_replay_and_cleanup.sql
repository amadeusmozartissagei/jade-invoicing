-- Phase 1 slice (b): what idempotent replay needs beyond the first schema
-- (ADR 0014).
--
-- 1. response_headers: a replay must return the same Location (and, for
--    invoices, ETag) as the first response, not only its status and body.
--    The API stores an allowlist of headers (Content-Type, Location, ETag);
--    Request-Id is per request and never stored.
-- 2. A daily pg_cron job deletes expired keys. Their response bodies can hold
--    personal data (a created customer), so they must not outlive the 24 hours
--    the contract promises. The API also ignores and replaces an expired key
--    on its own, so the job is about data minimisation, not correctness.

alter table app.idempotency_keys
  add column response_headers jsonb not null default '{}'
    check (jsonb_typeof(response_headers) = 'object');

-- Runs as postgres (the pg_cron job's owner), which bypasses RLS: one call
-- cleans every tenant. jade_api cannot call it.
create function app.delete_expired_idempotency_keys() returns bigint
language sql
set search_path = ''
as $$
  with gone as (
    delete from app.idempotency_keys where expires_at <= now() returning 1
  )
  select count(*) from gone;
$$;

revoke execute on function app.delete_expired_idempotency_keys() from public;

-- cron.schedule with a job name replaces a job of the same name, so applying
-- this again (or on a fresh project) leaves exactly one job.
select cron.schedule(
  'jade-delete-expired-idempotency-keys',
  '41 3 * * *',
  'select app.delete_expired_idempotency_keys()'
);
