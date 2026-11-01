-- Spike only: every 10 seconds, pg_cron uses pg_net to call the Go drain
-- endpoint on Vercel. ${...} placeholders are filled from .env.local by
-- cmd/runsql, so the token is stored in cron.job, not in this repository.
-- A real setup would read the token from Supabase Vault instead.
select cron.schedule(
  'spike-drain',
  '10 seconds',
  $job$
  select net.http_get(
    url := '${SPIKE_BASE_URL}/api/queue?action=drain',
    headers := jsonb_build_object('Authorization', 'Bearer ${SPIKE_TOKEN}'),
    timeout_milliseconds := 10000
  );
  $job$
);
