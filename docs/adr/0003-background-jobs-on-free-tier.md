# 0003. Background jobs: pgmq queue, drained by Go on Vercel, triggered by pg_cron

- **Status:** Accepted
- **Date:** 2026-11-01
- **Decided by:** Hamza Pratama (owner), based on a spike run by the AI coding agent

## Context

The platform needs background work: webhook delivery with retries, and later retention jobs. ADR 0001 chose Vercel Hobby and Supabase Free and left three things unconfirmed: whether Supabase Free allows `pgmq`, `pg_cron`, and `pg_net`; whether Go functions on Vercel can use the Supabase connection pooler; and how scheduled work reaches the Go code, given that Vercel Hobby cron runs only once a day.

A spike tested this on 2026-11-01. The throwaway code is on branch `spike/free-tier` (`spikes/free-tier/`).

### Setup

- Supabase project on the Free plan, region Frankfurt (eu-central-1), Postgres 17.11. Region chosen so that data stays in the EU.
- Project settings: Data API **off** (all data access goes through the Go API), automatically expose new tables **off**, automatic RLS on new tables **on**.
- Vercel Hobby project `jade-invoicing-spike`, region `fra1`, Go runtime (beta), `pgx` v5.12.

### Findings

| Question | Result |
|---|---|
| Extensions on Supabase Free | `pg_cron` 1.6.4, `pg_net` 0.20.4, and `pgmq` 1.5.1 all work. |
| Enabling `pgmq` | Enabling it from Database → Extensions failed with `3F000: schema "pgmq" does not exist`. `create extension if not exists pgmq;` (without `with schema`) works. The migration `20261101060834_enable_extensions.sql` does it that way. |
| Go on Vercel → Supabase | Works through the transaction pooler (port 6543) with `pgx` in simple-protocol mode, which the pooler requires because it does not support prepared statements. |
| Database latency from `fra1` | First query on a new instance, including the connection: 26 ms. Warm queries: 1 to 2 ms. |
| Cold start | Not measured cleanly. The first request after deploy reached an instance that was already 12 s old. Client-side times (0.3 to 1.1 s) were measured from Indonesia and mostly show network distance. |
| Queue operations | `pgmq.send`, `pgmq.read` with a 30 s visibility timeout, and `pgmq.archive` all work through the pooler. |
| Scheduler | A `pg_cron` job with a 10-second schedule ran on time (every 10 s). Each run took 2 to 13 ms, because `pg_net` only queues the HTTP request. |
| Database → Vercel | `pg_net` calls to the Go endpoint returned HTTP 200 with no timeouts. |
| End-to-end lag | 4.4 to 9.1 s from `send` to processing (three samples), as expected from a 10 s polling interval. |
| Access control | Requests without the bearer token got HTTP 401. |

## Options

**A. `pg_cron` + `pg_net` call a Go endpoint that drains `pgmq`.** All scheduling stays in the database, which is free. Work is pulled from the queue, so a failed or slow call leaves messages in the queue.

**B. Vercel Daemons** (a container that pulls from the queue continuously). On Hobby, provisioned memory runs out in about 9 to 17 days of continuous running (ADR 0001), and Daemons are in beta.

**C. Vercel Cron.** Hobby allows one run per day. Not usable for retries.

**D. Supabase Edge Functions** for the worker. Free, but adds a second runtime (Deno/TypeScript) next to the Go API.

## Decision

Option A. Work goes into `pgmq` queues. A `pg_cron` job calls a Go endpoint on Vercel through `pg_net` every few seconds. The endpoint reads a batch, does the work, and archives each message only after it succeeds. Messages that fail become visible again after the visibility timeout, and `read_ct` drives retry limits and backoff.

## Consequences

**Positive**

- Runs at $0 and needs only the services already chosen.
- The queue is durable: if Vercel is down, messages wait; if a function dies mid-batch, messages reappear after the visibility timeout. This gives at-least-once delivery, so every handler must be idempotent.
- The design is easy to explain: the database owns state and timing, the Go code owns the work.

**Negative, and how we handle it**

- **Latency is up to one interval** (10 s in the spike). Acceptable for webhooks. The interval is a tuning knob.
- **Quota use.** A 10 s interval means about 259,000 calls per month, within Vercel Hobby's 1,000,000 invocations. Active CPU (4 hours per month on Hobby) was not measured per call; it must be checked before the first public demo, and the interval raised (for example to 30 s) if needed.
- **The trigger endpoint is public.** It is protected by a bearer token. In the spike the token sat in the `cron.job` command text; the real setup should keep it in Supabase Vault.
- **Housekeeping.** `cron.job_run_details` grows with every run and needs a cleanup job. `pg_net` responses expire on their own.
- **Beta dependency** on the Vercel Go runtime remains (ADR 0001).
- **Dashboard pitfall.** Extensions must be enabled through migrations, not the dashboard, so new environments match.

## Follow-ups

- Measure Active CPU per drain call and cold-start time from the Vercel dashboard before the first public demo.
- Design the webhook delivery table, retry schedule, and dead-letter handling on top of `read_ct`.
- Store the trigger token in Supabase Vault.
- Add a cleanup job for `cron.job_run_details`.
