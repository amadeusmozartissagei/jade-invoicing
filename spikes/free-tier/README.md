# Free-tier spike (throwaway)

Checks that the free stack works end to end: Go on Vercel (`fra1`) → Supabase transaction pooler → `pgmq`, with `pg_cron` + `pg_net` calling the Go endpoint on a schedule. Results go into ADR 0003. This folder is not merged into `main`.

## Endpoints

All need `Authorization: Bearer $SPIKE_TOKEN`.

- `GET /api/health`: pool setup time, one query's round trip, and whether the instance was cold.
- `POST /api/queue?action=send&n=3`: puts test messages on the `spike` queue.
- `GET /api/queue?action=drain`: reads up to 10 messages, then archives them.

## Running

```bash
go run ./cmd/runsql -f sql/setup.sql      # create the queue
go run ./cmd/runsql -f sql/schedule.sql   # start the 10-second cron job
go run ./cmd/runsql -c "select jobid, status, return_message, start_time from cron.job_run_details order by start_time desc limit 5"
go run ./cmd/runsql -f sql/cleanup.sql    # stop the job, drop the queue
```

`runsql` reads `../../.env.local` (see `.env.example` at the repository root).
