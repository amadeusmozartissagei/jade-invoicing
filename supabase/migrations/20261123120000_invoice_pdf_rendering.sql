-- Phase 1 slice (e): rendering invoice PDFs (ADR 0017).
--
-- 1. A private Storage bucket for issued documents' files (P5, D6). Buckets
--    are rows, not schema objects, so the CLI's diff never creates them; the
--    insert keeps `db push` and `db reset` identical everywhere.
-- 2. The render_pdf queue. Issuing a document sends one message with
--    tenant_id and invoice_id; the drain endpoint renders, uploads, and
--    inserts the invoice_files row (P1, D1, D2).
-- 3. A pg_cron job that calls the drain endpoint every 10 seconds, but only
--    when Vault holds its URL and token and a message is visible (P7, D5).

-- ---------- 1. bucket ----------

-- 10 MB is far above a rendered invoice (about 35 KB in the spike) and below
-- the Free plan's 50 MB upload limit. Phase 2 adds the XML types.
insert into storage.buckets (id, name, public, file_size_limit, allowed_mime_types)
values ('invoice-files', 'invoice-files', false, 10 * 1024 * 1024, array['application/pdf'])
on conflict (id) do nothing;

-- No policies on storage.objects for this bucket: only the server's S3
-- access key (which bypasses RLS inside Storage) reads and writes it, and
-- clients download through presigned URLs.

-- ---------- 2. queue ----------

-- Same rights as webhook_deliveries (20261102120300): pgmq's functions run
-- with the caller's rights. Messages carry only IDs, because pgmq's tables
-- have no RLS.
select pgmq.create('render_pdf');

grant select, insert, update, delete on pgmq.q_render_pdf to jade_api;
grant select, insert on pgmq.a_render_pdf to jade_api;

-- ---------- 3. schedule ----------

-- Calls the drain endpoint when there is work. The URL and token live in
-- Vault (names drain_url, drain_token), set by hand per environment, so the
-- token never appears in cron.job. Without them, as locally and in CI, this
-- does nothing. Returns the pg_net request id, or null when it did not call.
-- Runs as postgres (the pg_cron job's owner); jade_api cannot call it.
create function app.request_queue_drain() returns bigint
language plpgsql
set search_path = ''
as $$
declare
  drain_url   text;
  drain_token text;
begin
  -- Only queues the drain endpoint handles; slice (f) adds
  -- webhook_deliveries here.
  if not exists (select 1 from pgmq.q_render_pdf where vt <= now()) then
    return null;
  end if;
  select decrypted_secret into drain_url from vault.decrypted_secrets where name = 'drain_url';
  select decrypted_secret into drain_token from vault.decrypted_secrets where name = 'drain_token';
  if drain_url is null or drain_token is null then
    return null;
  end if;
  return net.http_post(
    url := drain_url,
    headers := jsonb_build_object('Authorization', 'Bearer ' || drain_token, 'Content-Type', 'application/json'),
    body := '{}'::jsonb,
    timeout_milliseconds := 25000
  );
end;
$$;

revoke execute on function app.request_queue_drain() from public;

select cron.schedule('jade-request-queue-drain', '10 seconds', 'select app.request_queue_drain()');
