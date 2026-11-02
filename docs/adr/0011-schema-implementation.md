# 0011. Schema implementation: the API role, money columns, the decimal library, and sqlc

- **Status:** Accepted
- **Date:** 2026-11-02
- **Decided by:** Hamza Pratama (owner) for the role's password and the decimal library; the AI coding agent for the money columns, the error codes, and how `sqlc` is installed, recorded here for his review

## Context

Phase 0, step 5 turns the data model (ADR 0007, [`docs/design/data-model.md`](../design/data-model.md)) into migrations, with a test for each of its nine acceptance criteria, and sets up `sqlc` with the first queries (ADR 0005). Earlier ADRs left facts to check before writing the SQL: whether a custom login role works through the Supabase transaction pooler, which grants `pgmq` needs, what default privileges Supabase applies to a new schema, and whether the chosen decimal library rounds half away from zero.

### Facts checked on 2026-11-02

| Question | Result |
|---|---|
| Custom role through the transaction pooler | A temporary role `jade_probe` (`LOGIN`, `NOBYPASSRLS`, random password) was created on the Supabase project, used, and dropped. It connected to port 6543 as `jade_probe.<project_ref>` with `pgx` in exec mode; `current_user` was `jade_probe`, `set_config(..., true)` worked inside a transaction, and 50 parameterized queries had no errors. |
| Default privileges for a new schema | The project and the local stack have the same `pg_default_acl`: entries for `public`, `storage`, `graphql`, and other Supabase schemas, none global. A new schema `app` gets no grants for `anon` or `authenticated`. But Postgres lets `PUBLIC` execute every new function, and a per-schema `ALTER DEFAULT PRIVILEGES ... REVOKE` cannot remove that: per-schema defaults only add to the global ones ([PostgreSQL 17, ALTER DEFAULT PRIVILEGES][adp]). The first draft of the migration relied on it; a catalog query showed `anon` could still execute the `app` functions. |
| Other project defaults | The project has an event trigger `ensure_rls` that enables RLS on new tables in `public` only. It does not touch `app`. `postgres` has `BYPASSRLS` on Supabase, so `FORCE ROW LEVEL SECURITY` does not bind it; only `jade_api` is under RLS. |
| `pgmq` grants | `pgmq` 1.5.1's functions run with the caller's rights. A non-owner role needs `USAGE` on schema `pgmq`, `SELECT, INSERT, UPDATE, DELETE` on the queue table `q_<name>`, and `SELECT, INSERT` on the archive table `a_<name>` (archive uses `INSERT ... RETURNING`). Without them `send` fails with `permission denied for table q_...`. |
| `numeric(18,2)` | A column with a scale rounds its input: inserting `1.001` stores `1.00` with no error, before any check constraint or trigger sees the value. |
| `shopspring/decimal` | `Round` is half away from zero (`0.125` → `0.13`, `-2.5` → `-3`); `RoundUp` and `RoundDown` are away from and toward zero. v1.5.0's listed behaviour changes do not touch `Round`. `Decimal` implements `sql.Scanner` and `driver.Valuer`, and round-trips through `pgx` exec mode exactly without a registered codec. |
| `pgx` exec mode | Reading `pgx` v5.12.0 (`conn.go`): exec mode sends parameters as text with no type OIDs, so the server infers each type from the SQL. Ambiguous parameters need a cast in the query. |
| `sqlc` | v1.31.1 builds on Windows without cgo and parses the migrations as they are (`create extension`, PL/pgSQL functions, policies, `select pgmq.create(...)`). It cannot see the columns of a function that `returns table`, so the API key lookup is written by hand. |

## Options

### The role's password

**A. Created without a password in the migration; set once per environment outside the repository.** No secret in the repository or the migration history. The project's password is set by hand, and tests switch to the role with `SET ROLE` instead of logging in.

**B. Supabase Vault.** The password still has to reach Vault from outside, and the migration gets more complex.

### Decimal library

**A. `shopspring/decimal` (MIT).** The most widely used decimal type in Go; explicit `Round`, `RoundUp`, `RoundDown`.

**B. `cockroachdb/apd` v3 (Apache-2.0).** Arbitrary precision with an explicit context and rounding mode; stricter but more verbose, and less common in application code.

### Money columns

**A. `numeric(18,2)`, as the design document said.** Caps the magnitude and the decimals, but by rounding, so a Go bug that produced `0.125` would be stored as `0.13` without an error.

**B. Plain `numeric`, with `app.fits_currency(amount, currency)` in the checks and the child-table triggers.** The function requires at most 16 integer digits (the contract's limit) and no more decimals than the currency's minor unit, so `0.125` EUR and `0.5` JPY are both rejected.

### Installing `sqlc`

**A. A `tool` directive in `apps/api/go.mod`, like `oapi-codegen`.** One version everywhere, but it adds `sqlc`'s many dependencies (its SQL parser and a WebAssembly runtime) to the API module, and building it from source took about seven minutes here.

**B. A pinned release binary.** CI uses `sqlc-dev/setup-sqlc` pinned to a commit SHA with the version in an environment variable, as with `golangci-lint`; contributors install the same version.

## Decision

- **Role: Option A**, chosen by the owner. The migration creates `jade_api` with `LOGIN NOBYPASSRLS NOINHERIT` and no password, and grants `postgres` the right to `SET ROLE jade_api` (not to inherit its rights). The project's password is set by hand (`apps/api/README.md`).
- **Decimal library: Option A**, `shopspring/decimal` v1.6.0 (MIT), chosen by the owner. A test pins the rounding behaviour.
- **Money columns: Option B.** This changes the design document's `numeric(18,2)`; the reason is the rounding found above. Quantities and unit prices keep `numeric(18,6)`, since rounding beyond six decimals there cannot change an amount the contract allows.
- **`sqlc`: Option B**, v1.31.1, generating into `apps/api/internal/store`. CI's existing check that generated code is up to date now covers it.
- **Every migration that creates functions in `app` ends with `REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA app FROM PUBLIC`** (the three that do so far all end with it), and the test for criterion 9 lists any privilege `anon` or `authenticated` has on a schema, table, sequence, or function in `app`.
- **Trigger errors have their own SQLSTATEs** in a class no standard code uses: `JD001` (an issued document or an insert-only row was changed), `JD002` (a payment for an invoice that cannot be paid), `JD003` (too many decimals for the currency). Go and the tests tell them apart from other failures by code, not by message text.
- **Keys and series get only the rights the contract needs** (from the security review): `jade_api` may update an API key's `revoked_at` and `last_used_at` and a series' `next_value`, nothing else. Triggers add what a grant cannot: revocation is final, and `next_value` only moves up by one, so a bug cannot skip invoice numbers. Both hold for `postgres` too.
- **Recording a key's use does not count as an edit:** the `api_keys` `updated_at` trigger ignores changes to `last_used_at` only.
- **RLS policies call `(select app.current_tenant_id())`**, so Postgres evaluates the setting once per statement instead of once per row.

## Consequences

**Positive**

- The role, the grants, and the default-privilege trap were checked on the real project before the SQL depended on them, so D2 stands as decided.
- Each of the nine acceptance criteria has a test that runs as `jade_api`. Each was checked by breaking the invariant it covers (disabling a trigger, adding a permissive policy, granting `anon` a function, dropping a constraint, making the key lookup `security invoker`) and seeing the test fail.
- An amount with too many decimals is rejected at every layer: the contract's pattern, and now the database too.

**Negative, and how we handle it**

- **Before the API can use the Supabase project**, the migrations must be pushed and `jade_api`'s password set by hand. Until then only the local stack and CI have the schema.
- **The test pool switches roles with `SET ROLE`**, which is session state and needs a direct connection. Running the schema tests through the transaction pooler needs a real `jade_api` login instead.
- **`postgres` may `SET ROLE jade_api`.** Harmless, because `postgres` already has more rights, but it is a grant that exists for the tests.
- **A new function in `app` is executable by everyone until revoked.** The criterion 9 test fails if a migration forgets.
- **`sqlc` is pinned in two places** (the CI environment and the README). CI uses the Linux release binary, locally it is built from source without cgo; the drift check would show if their output ever differed.
- **RLS protects against bugs, not against SQL injection.** `jade_api` can run `set_config('app.tenant_id', ...)` itself, so injected SQL could choose any tenant. Parameters only, never SQL built from strings, stay the defence there (checked in review).
- **Plain `numeric` has no column-level cap**, so the cap lives in one function that every amount check calls.

## Follow-ups

- Push the migrations to the Supabase project, set `jade_api`'s password, and run the criterion 9 check there.
- Run the schema tests through the transaction pooler with a real `jade_api` login (ADR 0005 follow-up).
- Decide how the API writes to Supabase Storage without the service-role key (with the PDF slice).
- Webhook slice: the URL check in the database only requires `https://`. The sender must refuse private, loopback, and link-local addresses after DNS resolution and on every redirect (SSRF).
- Review findings left open (2026-11-02): the child-table trigger reads the parent once per row (consider a statement-level trigger if draft writes get slow); a global `ALTER DEFAULT PRIVILEGES FOR ROLE postgres REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC` could replace the per-migration revoke once its effect on Supabase's own schemas is checked.

[adp]: https://www.postgresql.org/docs/17/sql-alterdefaultprivileges.html
