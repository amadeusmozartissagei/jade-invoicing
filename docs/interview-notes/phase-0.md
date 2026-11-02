# Interview notes: phase 0 (foundation)

Likely questions about the foundation of jade-invoicing, with short answers in plain language. Each answer names the document that has the full reasoning. Phase 0 ended with the schema migration (step 5); the design quiz, held once development is done, revises any answer that turns out shaky.

## The project

**What is it, in one sentence?**
An open-source, multi-tenant API that lets a company issue invoices that follow Japanese and German rules: correct tax, numbering, required fields, a PDF, and for Germany a structured e-invoice.

**Why invoicing, and why Japan and Germany?**
I am applying for jobs in both countries. Both have new invoice rules: Japan's Qualified Invoice System since October 2023, and German B2B e-invoicing, which every business must be able to receive since 2025. Fintech is the biggest employer on English-speaking Japanese job boards, and German employers care about data protection. The project started as e-KYC, but its AI part could not run on free hosting, so I switched (ADR 0001).

**Why must it run for free?**
It is a portfolio project that has to stay online for months without a budget. The constraint also forces real design work: the background-job system exists because Vercel's free cron runs only once a day (ADR 0001, ADR 0003).

**Did you use AI to build it?**
Yes, an AI coding agent writes most of the code and drafts the documents. I set the goals, make every decision from options the agent researches, and review the work. Every decision has an ADR, tests and CI check the code, and `docs/ai-workflow.md` records what the agent did and what I decided. I have to be able to explain every part, which is what these notes are for.

## Architecture

**What are the parts?**
A Go API on Vercel, a Next.js dashboard later, and Supabase for Postgres, file storage, and auth. Background work (webhooks, PDF rendering) runs from Postgres queues. The OpenAPI file and the database migrations sit at the root of the repository because both apps share them (ADR 0004).

**Why Go?**
It is common in Japanese fintech, it starts fast on serverless platforms, and the standard library covers HTTP routing since Go 1.22, so the API needs few dependencies (ADR 0005).

**How do background jobs work without a paid cron?**
Jobs go into a `pgmq` queue inside Postgres. `pg_cron` runs every few seconds and calls a Go endpoint on Vercel through `pg_net`. The endpoint reads a batch, does the work, and archives each message only after it succeeds. If the worker crashes, the message becomes visible again after a timeout and is retried (ADR 0003).

*What fails if Vercel is down?* Messages stay in the queue and are processed when it is back. *If Postgres is down?* Nothing works, including the API; that is the single point of failure on a free tier, and it is stated openly.

**Why one Go server and not one function per endpoint?**
Vercel's Go runtime can now run a whole HTTP server. One server means one cold start and one connection pool per instance, and normal middleware. One function per file would mean a pool and a cold start per endpoint (ADR 0004).

## Database

**Why `pgx` and `sqlc`, not an ORM?**
Invoice logic needs exact SQL: row locks for numbering, several statements in one transaction, and a per-transaction setting for row-level security. With `sqlc`, I write the SQL and it generates type-safe Go, checked against the schema at build time (ADR 0005).

**The Supabase pooler does not support prepared statements. How do you handle that?**
`pgx` runs in `QueryExecModeExec`: parameters are still sent separately from the SQL, but without a named prepared statement. I tested it through the real pooler with parallel queries, and the skeleton has two smoke tests that pass against it. The fallback is the simple protocol (ADR 0005).

**How do you keep tenants apart?**
In the database, not only in Go. Every request runs in a transaction that sets the tenant with `set_config(..., true)`, which lasts only until the transaction ends, so it is safe with a transaction pooler. Row-level security policies compare each row's `tenant_id` with that setting, and the API connects as a role that cannot bypass RLS. If the setting is missing, queries return no rows instead of everyone's rows (ADR 0007).

**How is money stored?**
As `numeric`, never as floating point, and as decimal strings in JSON (`"1190.00"`), because many JSON parsers turn numbers into binary floats that cannot hold `0.10` exactly. Amounts with more decimals than the currency allows are rejected, not rounded, by the contract and again by a database check. The columns are plain `numeric`, not `numeric(18,2)`, because a column with a scale silently rounds `0.125` to `0.13` before any check runs; I found that in a smoke test (ADR 0007, ADR 0008, ADR 0011).

**How do you make invoice numbers sequential without gaps?**
The number is assigned when the invoice is issued, from a counter row that the issue transaction locks and updates. If the transaction rolls back, the increment rolls back too, so there are no gaps and no duplicates (ADR 0007, D6).

**Can an issued invoice be changed?**
No. Database triggers reject changes to issued invoices, their lines, and their party snapshots; only the status can move to voided or corrected. Corrections are new documents: a credit note or a corrected invoice that refers to the original (ADR 0007).

## API contract

**Why write the OpenAPI file by hand first?**
The contract is the agreement with clients and with the dashboard. Writing it first let me fix errors, idempotency, and pagination before any code, and Go types and the server interface are generated from it, so the code cannot silently drift (ADR 0008).

**What happens if a client retries a `POST` after a timeout?**
Every `POST` needs an `Idempotency-Key`. The successful response is stored in the same transaction as the change, so a retry gets the stored response instead of a second invoice. A concurrent retry with the same key gets `409`, detected with a transaction-scoped advisory lock (ADR 0008).

**How are errors returned?**
As RFC 9457 problem details, with a stable `code` and, for validation errors, a list of fields with JSON Pointers. A `500` never contains stack traces or SQL; the request ID links it to the server log (ADR 0008, `docs/api/errors.md`).

**How do webhooks work?**
Each event is signed with HMAC-SHA256 using the Standard Webhooks scheme, and contains no personal data; the receiver fetches details through the API. Failed deliveries are retried 10 times over about three days, then marked dead and can be redelivered by hand (ADR 0009).

## Skeleton, tooling, and CI

**What does the skeleton contain?**
A Go server with a health endpoint, request IDs, panic recovery, structured logging, a 1 MiB body limit, and problem-details responses for unknown routes and wrong methods. It is deployed to Vercel in Frankfurt (ADR 0010).

**How do you know the server follows the contract?**
Tests send real requests to the handler and check the responses against `openapi.yaml` with `libopenapi-validator`. A separate test makes sure the checker actually rejects a broken response, so the contract tests cannot pass by accident (ADR 0010).

**What does CI check?**
The OpenAPI file is linted; the Go code is linted, vetted, built, and checked for stale generated code; and the tests run with the race detector against the same Postgres image Supabase uses, with the migrations applied. Database tests are skipped on a laptop without a database, but fail in CI if the database is missing, so a broken CI setup cannot hide them (ADR 0010).

**Did anything go wrong setting up the local stack?**
Yes: on Windows the database container could not bind port 54322. Supabase's default ports sit inside Windows' dynamic port range, and Hyper-V had reserved a block that included them. Running `netsh interface ipv4 show excludedportrange protocol=tcp` showed the reserved ranges. Instead of an admin workaround that can break again after a reboot, I moved the local ports to 443xx, outside the dynamic range, and wrote the reason into `supabase/config.toml`.

**Why pin GitHub Actions to commit SHAs?**
A tag like `v7` can be moved to point at different code, so a compromised action could run in my CI without any change in my repository. A SHA cannot be moved.

**Why is `/healthz` outside `/v1` and why does it not check the database?**
It is for operations, not clients, so it is not part of the versioned contract. It does not touch the database so that it stays cheap and answers even while the free database is paused.

## Schema and its tests (step 5)

**How do you prove that one tenant cannot see another's data?**
A test creates two tenants, gives each a row in every tenant table, and then, running as the API's role with tenant A set, counts the rows of other tenants in each table: always zero. With no tenant set, every table returns nothing. The list of tables comes from the catalog, so a table added later is covered automatically, and the test fails if the fixture gives it no rows. It also checks that every table has forced RLS and the same policy, and that writing a row for tenant B is rejected (ADR 0011, criterion 1).

**The tests connect as `postgres`, which bypasses RLS. How do they test as the API?**
Each connection in the test pool runs `SET ROLE jade_api` first, so every query runs with exactly the API role's rights: no `BYPASSRLS`, only its grants. The production code path (`db.InTenantTx`) is the same. `SET ROLE` is session state, so this needs a direct connection, not the transaction pooler.

**How do you know the tests would catch a bug?**
I broke each invariant on purpose and ran its test: disabled the immutability trigger, added a policy that lets everyone see customers, turned RLS off on one table, granted `anon` a function, dropped the totals constraint and the one-correction index, and made the API key lookup run with the caller's rights. Every test failed, then passed again after a database reset. A test that cannot fail proves nothing.

**What happens when 50 requests issue invoices on the same series at once?**
They queue on the series row: each issue updates the counter in its transaction and holds the row lock until commit. The test runs 50 concurrent issues and checks the numbers are exactly 1 to 50, then rolls one issue back and checks the next number has no gap (criterion 4).

**How can the API find an API key before it knows the tenant, if RLS hides every row?**
Through one function, `app.find_api_key(prefix)`, that is `security definer`: it runs with its owner's rights and can read across tenants, but returns only the one key with that prefix and only the columns authentication needs. Its `search_path` is empty so a planted object cannot hijack it, `PUBLIC` cannot execute it, and a test checks it is the only such function the API role can run (criterion 7).

**What was the default-privilege trap?**
Postgres lets everyone execute a new function unless that is revoked. I first wrote `ALTER DEFAULT PRIVILEGES IN SCHEMA app REVOKE EXECUTE ... FROM PUBLIC`, but a per-schema default can only add to the global defaults, never remove from them, so it did nothing. A catalog query showed `anon` could still execute the functions. Now every migration that adds functions revokes them explicitly, and a test lists any privilege `anon` or `authenticated` has in the schema (criterion 9).

**Why do triggers raise their own error codes?**
So Go and the tests can tell "an issued invoice was changed" (`JD001`) from "a payment for an invoice that cannot be paid" (`JD002`) or "too many decimals" (`JD003`) without parsing messages. The triggers are a safety net: from phase 1, Go checks the same rules first and answers with a proper `409`, so a trigger error reaching a client will mean a bug (ADR 0011).

**Why does the policy say `(select app.current_tenant_id())` instead of calling the function directly?**
Wrapped in a scalar subquery, Postgres evaluates it once per statement and reuses the value; called directly in the policy, it can be evaluated once per row.

**Does row-level security protect you against SQL injection?**
No. The API role sets the tenant itself with `set_config`, so injected SQL could pick any tenant. RLS catches the common bug, a query that forgets the tenant; injection is stopped by passing every value as a parameter. The security review also narrowed what the role can update: it can revoke a key but not un-revoke it or replace its hash, and it can move a number series up by one but not skip ahead (ADR 0011).

**Why is the API role's password not in the migrations?**
Migrations are in a public repository and run in every environment. The migration creates `jade_api` without a password, so it cannot log in until someone sets one by hand on that environment (ADR 0011).
