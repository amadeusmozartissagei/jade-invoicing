# 0005. Go HTTP routing with net/http; database access with pgx v5 (exec mode) and sqlc

- **Status:** Accepted
- **Date:** 2026-11-01
- **Decided by:** Hamza Pratama (owner), with research by the AI coding agent

## Context

The Go API runs as one HTTP server on Vercel (ADR 0004). It needs a router and middleware (request IDs, panic recovery, logging, API-key auth, timeouts), and a way to talk to Postgres.

Constraints:

- The database is reached through the Supabase transaction pooler, which does not support named prepared statements. ADR 0003 used `pgx` in simple-protocol mode for this. `pgx` v5 has another mode that also avoids named prepared statements, `QueryExecModeExec`, and its own documentation says to prefer that over the simple protocol, which "should only be used if connecting to a proxy server, connection pool server, or non-PostgreSQL server that does not support the extended protocol" (`pgx` v5.12.0, `conn.go`).
- Invoice logic needs exact control over SQL: row locks for per-tenant numbering, transactions that span several statements, and transaction-local settings for row-level security.
- Schema changes are plain SQL files in `supabase/migrations/` (ADR 0003).
- Open-source dependencies only, with their licenses recorded.

Since Go 1.22, the standard `http.ServeMux` matches on method and path wildcards (`POST /v1/invoices/{id}/issue`, read with `r.PathValue("id")`) and answers 405 with an `Allow` header for a known path with the wrong method (`net/http/server.go`, Go 1.27).

### Pooler test (2026-11-01)

A throwaway program ran read-only queries against the Supabase transaction pooler (port 6543) with `pgx` v5.12.0 in `QueryExecModeExec`, from a pool of 4 connections:

| Check | Result |
|---|---|
| 200 parameterized queries from 8 goroutines (`bigint`, `text`, `timestamptz`, `numeric`) | 0 errors, all results correct |
| `set_config('app.tenant_id', $1, true)` in a transaction | Visible inside the transaction; empty on the next query after commit |
| `text[]` and `uuid` parameters | Correct |

This shows the mode works through the pooler for these types. It is a small sample from one client, not a load test.

## Options

### Routing

**A. Standard library `net/http`.** No dependency. Middleware is a small `func(http.Handler) http.Handler` chain written in the project.

**B. `chi` v5 (MIT).** Route groups and ready-made middleware. Still `net/http`-compatible, so handlers look the same.

**C. Gin, Echo, or Fiber.** Own context types and conventions; Fiber is not `net/http`-based. More to unlearn and less portable.

### Database access

**A. `pgx` v5 with `sqlc`.** Queries are written as SQL files; `sqlc` (MIT) reads the migrations and generates type-safe Go functions. Supports `pgx/v5` directly. ([sqlc with pgx][sqlc-pgx])

**B. `pgx` v5 only, all queries by hand.** Most transparent, but every query needs manual scanning, and nothing checks SQL against the schema before run time.

**C. An ORM (GORM, ent).** Fast CRUD, but the SQL is hidden, and row locks, RLS settings, and exact numeric handling become harder to see and to explain.

## Decision

- **Routing: Option A, the standard library.** The middleware the API needs is a few dozen lines, and writing it shows how it works. If the OpenAPI contract (phase 0, step 3) is turned into Go code, `oapi-codegen` (Apache-2.0) can generate for plain `net/http` (`std-http-server`, Go 1.24+) and supports OpenAPI 3.1. ([oapi-codegen][oapi-codegen]) Whether to generate is decided with the contract.
- **Database: Option A, `pgx` v5 with `sqlc`.**
  - One `pgxpool.Pool` per server instance with `DefaultQueryExecMode = QueryExecModeExec`, and a small `MaxConns`, since many instances may share the pooler. Parameters travel separately from the SQL text, and each query takes one round trip. If a query type turns out not to work through the pooler, fall back to `QueryExecModeSimpleProtocol`, which the spike proved (ADR 0003).
  - Queries live in `.sql` files inside `apps/api`; `sqlc` reads `supabase/migrations/` as the schema. Generated code is committed, and CI fails if it is out of date.
  - Queries that `sqlc` handles badly, mainly list endpoints with optional filters, are written by hand with `pgx` next to the generated code.
  - Each request that touches tenant data runs in one transaction that first sets the tenant with `set_config(..., true)`. That setting is local to the transaction, so it is safe with a transaction pooler, and RLS policies can read it. The exact roles and policies are part of the schema spec (phase 0, step 2).

## Consequences

**Positive**

- Few dependencies (`pgx` MIT, `sqlc` MIT, and `oapi-codegen` Apache-2.0 if used), all widely known.
- The SQL that runs is the SQL in the repository, which makes locking, numbering, and RLS easy to review and to explain in an interview.
- `sqlc` catches mismatches between queries and the schema at generation time instead of in production.

**Negative, and how we handle it**

- **Exec mode guesses parameter types from Go types**, and sends them as text. `[]byte` becomes `bytea`, so JSON must be passed as `string`, and ambiguous types (for example a `map[string]string`) are rejected. User-defined types may need registering with `pgx` or an explicit cast in SQL; found and fixed when the schema is written.
- **If the fallback is ever needed**, the simple protocol makes `pgx` quote and inline parameters on the client. That is still safe against SQL injection as long as values only go through parameters, never string building, which code review checks either way.
- **One transaction per tenant request** adds `BEGIN` and `COMMIT` round trips. Warm queries from `fra1` took 1 to 2 ms in the spike (ADR 0003), so this costs a few milliseconds.
- **Hand-written middleware** has to be tested like any other code (recovery, timeouts, auth).
- **`sqlc` is a build-time tool** that every contributor and CI must install at a pinned version.
- **Two ways to write queries** (generated and hand-written). Hand-written ones stay in the same package and have tests against a real Postgres.

## Follow-ups

- Run the test suite against the pooler as well as local Postgres, so a query that breaks exec mode is caught before deployment.
- Pin `pgx` and `sqlc` versions in the skeleton (phase 0, step 4) and add the `sqlc` check to CI.
- Confirm that `sqlc` parses the Supabase migration files as they are (including `create extension` statements) or document what it needs.
- Choose the pool size after checking the Supabase Free pooler client limit.

[sqlc-pgx]: https://docs.sqlc.dev/en/latest/guides/using-go-and-pgx.html
[oapi-codegen]: https://github.com/oapi-codegen/oapi-codegen
