# 0010. API skeleton tooling: Go 1.27, libopenapi-validator, golangci-lint, and CI with a real Postgres

- **Status:** Accepted
- **Date:** 2026-11-02
- **Decided by:** Hamza Pratama (owner), with research and spikes by the AI coding agent

## Context

Phase 0, step 4 turns the contract (ADR 0008) and the layout (ADR 0004) into a runnable skeleton with CI. Three choices were left open by earlier documents:

- The Go version in `go.mod` (ADR 0004, follow-ups).
- A library that checks real responses against the OpenAPI 3.1 file in tests, and a request validation layer that produces the `errors` list with JSON Pointers (`docs/design/api.md`, "Open questions").
- How CI lints, tests, and builds, including tests that need Postgres with Supabase's extensions (`pgmq`, `pg_cron`, `pg_net`).

The owner decided on 2026-11-01: Docker Desktop for the local stack, the dashboard scaffold deferred until dashboard work starts, the full schema migration in a later step, and the API skeleton deployed now.

Facts checked on 2026-11-01 and 2026-11-02:

- Vercel "reads the Go version from the `go` directive in `go.mod`" and, the first time, downloads that toolchain ([Go runtime][vercel-go], updated 2026-08-11). A test deployment of the skeleton built with `Using Go 1.27.3 (from go.mod)` and served `/healthz` from `fra1`.
- The contract uses JSON Schema 2020-12 keywords, notably `unevaluatedProperties: false` on `allOf` compositions (for example `CustomerCreate`), so a 3.0-only validator would not do.

### Validator spike (2026-11-01)

Both candidate libraries loaded `openapi/openapi.yaml` and validated `POST /v1/customers` bodies and a `404` problem response:

| Case | `kin-openapi` v0.152.0 | `libopenapi-validator` v0.16.2 |
|---|---|---|
| Valid body | Passes | Passes |
| Unknown field `nmae` | Rejected, `/nmae`, "false schema" | Rejected, instance path `["nmae"]`, keyword `/unevaluatedProperties` |
| Missing `name`, bad enum, `name` over `maxLength` | Rejected, with paths in the message text | Rejected, with instance path and keyword location as fields |
| Problem response missing `code` | Not tested | Rejected |

Both libraries also showed the same trap: when a body has another error, they report the *valid* fields (`kind`, `name`) under `unevaluatedProperties` as well. That is correct JSON Schema 2020-12 behaviour (a failing subschema produces no annotations, so its properties count as unevaluated), but mapped naively it would tell a client that `name` is an unknown field.

## Options

### Contract validation library

**A. `libopenapi-validator` (pb33f, MIT).** Validates requests and responses against OpenAPI 3.0, 3.1, and 3.2. Each failure is a struct with the instance path, the JSON Path of the field, and the schema keyword that failed. Uses a maintained fork of `santhosh-tekuri/jsonschema` (Apache-2.0). Younger and less widely used than `kin-openapi`; still a 0.x version.

**B. `kin-openapi` (MIT).** The older and more widely used library; `oapi-codegen` itself depends on it, and its request-validation middleware is built on it. Targets OpenAPI 3.1. Errors are nested `MultiError` values whose paths have to be unwrapped, and request routing needs `gorilla/mux` or its legacy router.

**C. Hand-written checks only.** No dependency, but every `maxLength`, `pattern`, and enum in the spec would be written a second time in Go and could drift from it.

### CI database

**A. `supabase db start` in CI.** The same Postgres image and migrations as the local stack, with Supabase's extensions. About a minute of setup per run; the runner has Docker.

**B. A plain `postgres` service container.** Faster, but without `pgmq`, `pg_cron`, and `pg_net`, so migrations that create them would fail or need to be skipped.

## Decision

- **Go 1.27.3** in `go.mod`, the latest patch release, proven on Vercel.
- **Contract validation: Option A, `libopenapi-validator`.** Its flat, structured failures map directly onto the `errors` list (instance path to JSON Pointer, keyword to field-error code), and one library covers both request validation at run time and response validation in tests. In this step it is used in tests only (`internal/contracttest`). The request validation layer is built with the first endpoint in phase 1, on these rules:
  - The spec checks structure (types, required fields, lengths, patterns, enums, unknown fields); Go code checks domain rules the spec cannot express (currency decimals, valid tax rates for the supply date).
  - An `unevaluatedProperties` failure becomes `unknown_field` only for a property the schema does not declare, which removes the false reports described above.
  - If the library's behaviour gets in the way, `kin-openapi` is the fallback; the mapping layer is the only code that would change.
- **CI database: Option A.** Database tests skip when `TEST_DATABASE_URL` is unset, but fail when `CI` is set, so a broken CI setup cannot turn them into silent skips.
- **Linting: `golangci-lint` v2.15.0** with the standard linters plus `bodyclose`, `errorlint`, `gosec`, `noctx`, `rowserrcheck`, `sqlclosecheck`, and `gofmt`/`goimports`. It is GPL-3.0, which is fine for a tool that is run, not linked into the program. `oapi-codegen` v2.8.2 is pinned as a `tool` directive in `go.mod`, so `go generate` uses the same version everywhere.
- **CI** (`.github/workflows/ci.yml`): OpenAPI lint (`@redocly/cli` 2.72.1); API lint and build, including a check that `go generate` and `go mod tidy` leave no diff; API tests with `-race` against `supabase db start`. Third-party actions are pinned to commit SHAs, because a tag can be moved to different code.
- **Vercel project `jade-invoicing-api`**: Root Directory `apps/api`, Git integration on `main`, function region `fra1` from `vercel.json`. An `ignoreCommand` skips builds when neither `apps/api` nor `openapi/` changed (ADR 0004).

## Consequences

**Positive**

- The response shape of every tested endpoint is checked against the contract, and a self-test makes sure the checker really rejects a broken response.
- CI runs the same Postgres and extensions as the local stack and the Supabase project, so a migration that works in CI works on Supabase.
- `/healthz` answers without touching the database, so it works while a Free project is paused and costs nothing per call.

**Negative, and how we handle it**

- **`libopenapi-validator` is 0.x.** Breaking changes are possible; the version is pinned and its use is wrapped in two small packages.
- **The validator is only used in tests so far.** Run-time request validation, with the field-error mapping, is still to be built and tested in phase 1.
- **Local `-race` needs cgo**, which this Windows machine has no C compiler for; the race detector runs in CI only.
- **CI spends about a minute starting Postgres** on every run, including documentation-only commits. Free for a public repository; path filters can come later if it gets slow.
- **The local stack needs Docker Desktop** (about 4 to 5 GB). Only Postgres is started (`supabase db start`), not the full Supabase stack, to save disk space.

## Follow-ups

- Phase 1, first endpoint: the request validation middleware and the mapping to `errors`, with tests for the false `unevaluatedProperties` reports. Done 2026-11-02 (ADR 0012).
- Step 5: the schema migration and its acceptance tests run in the CI database set up here; set up `sqlc` with the first queries.
- Fixed 2026-11-02: the `ignoreCommand` compared the commit with the last deployed one, so a redeploy of the same commit (needed after changing an environment variable) was always canceled. It now builds when the two commits are the same.
- Run the database tests against the transaction pooler as well (ADR 0005). On 2026-11-01 the two smoke tests in `internal/db` passed against the Supabase pooler in exec mode.

[vercel-go]: https://vercel.com/docs/functions/runtimes/go
