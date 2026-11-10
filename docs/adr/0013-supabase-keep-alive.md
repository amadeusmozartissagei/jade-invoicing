# 0013. Keep the Supabase Free project awake with a scheduled query through `/readyz`

- **Status:** Accepted
- **Date:** 2026-11-10
- **Decided by:** Hamza Pratama (owner) decided to add the keep-alive now, before slice 7b. The AI coding agent made the design choices below and did the research, for his review.

## Context

On 2026-11-10 the Supabase project `jade-invoicing-dev` was found paused (status `INACTIVE`), about a week after the last work on 2026-11-02 and 2026-11-03. Every `/v1` call failed until the agent restored it through the Management API (`POST /v1/projects/{ref}/restore`; the Supabase CLI has no restore command). The restore took about four minutes, and all migrations were still there.

ADR 0001 already planned "a scheduled GitHub Actions job keeps the demo awake", and `docs/design/api.md` named `/healthz` as its target. But `/healthz` does not touch the database (ADR 0010), so pinging it would not count as activity.

Facts checked on 2026-11-10:

| Question | Result | Source |
|---|---|---|
| What counts as activity | "A Free plan project is considered inactive if it does not receive sufficient user database activity over the past week." "Typically a few user requests to the database each day over the previous week is enough." | [Project pausing][supabase-pausing] |
| Whether `pg_cron` jobs count | Not stated | [Project pausing][supabase-pausing] |
| Warning before a pause | An email roughly one week before the pause, and another once it is paused | [Project pausing][supabase-pausing] |
| Restore window | Up to one year after the pause | [Project pausing][supabase-pausing] |
| GitHub scheduled workflows | At most every 5 minutes; may be delayed or dropped under load, especially at the start of an hour; run on the default branch only; disabled after 60 days without repository activity in a public repository | [Events that trigger workflows][gh-schedule] |

## Options

**A. A `pg_cron` job inside the database.** No outside caller. But Supabase does not say whether its own scheduled jobs count as "user" activity, and it would not show whether the API can reach the database.

**B. A Vercel cron job.** Hobby allows one run a day, which is enough. But it would call our own function from Vercel, so the keep-alive would depend on the same platform it is supposed to watch, and a failure would be silent.

**C. A GitHub Actions schedule that calls a new `/readyz` endpoint, which runs one query as `jade_api` through the transaction pooler.** It runs the same path as a real client: Vercel, the pooler, a login as `jade_api`, a query. A failed run makes GitHub email the owner, so it doubles as an outage alert. It costs nothing on a public repository.

## Decision

Option C, as ADR 0001 planned, with the target changed from `/healthz` to a new `/readyz`. `/readyz` runs `select 1` through the API's pool with a 5-second deadline and answers `200 {"status":"ok","database":"ok"}`, or `503 {"status":"unavailable","database":"unavailable"}` with the cause only in the log. `/healthz` keeps its meaning: the process is up, whatever the database is doing.

`.github/workflows/keep-alive.yml` calls `/readyz` at 05:17 and 17:17 UTC, off the hour as GitHub advises, with three retries. It has no token permissions and uses no actions, so there is nothing to pin. It can also be run by hand (`workflow_dispatch`).

## Consequences

**Positive**

- The project gets two real database queries a day, well above "a few each week".
- A down API, a broken database login, or an already-paused project shows up as a failed run and an email, within half a day.
- `/readyz` is a readiness check for later monitoring (phase 5).

**Negative, and how we handle it**

- `select 1` may not be what Supabase counts as "sufficient user database activity". If the warning email arrives anyway, the fallback is to make `/readyz` write a heartbeat row, which needs a table and a grant.
- `/readyz` is public, and each call costs one query. The `/v1` routes already allow one unauthenticated query per call (ADR 0012), so this adds no new kind of exposure. While the database hangs, a burst of `/readyz` calls can hold the API's four pool connections for up to 5 seconds each and delay `/v1` requests on that instance. Rate limiting comes in phase 4.
- GitHub disables the schedule after 60 days without repository activity. While the project is being built that does not happen; after it, a commit every two months or a manual re-enable is needed. Noted in the follow-ups.
- Scheduled runs can be delayed or dropped. Missing a few of fourteen runs a week still leaves a wide margin.

## Follow-ups

- After the first scheduled runs, check them in the Actions tab.
- If Supabase sends a pause warning despite the job, switch `/readyz` to a heartbeat write.
- Once active development ends, re-enable the workflow or push a commit at least every 60 days.

[supabase-pausing]: https://supabase.com/docs/guides/platform/free-project-pausing
[gh-schedule]: https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#schedule
