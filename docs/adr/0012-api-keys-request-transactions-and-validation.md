# 0012. API key format and checks, one buffered transaction per request, and run-time validation from the embedded contract

- **Status:** Accepted
- **Date:** 2026-11-02
- **Decided by:** Hamza Pratama (owner) for the key encoding, the seller-profile scope, and the behaviour without a database URL. The AI coding agent made the other choices, recorded here for his review.

## Context

Phase 1 starts with what every endpoint depends on: creating a tenant and its first key, authenticating requests, running each request in its tenant's transaction (ADR 0005), and validating requests against the contract at run time (ADR 0010 follow-up). The slice proves these on `GET /v1/tenant` and `GET`/`PUT /v1/seller-profile`.

Fixed earlier:

- The key format is `jade_sk_<prefix>_<secret>`, with only SHA-256 of the secret stored (data-model.md).
- `app.find_api_key(prefix)` is the only lookup before the tenant is known (ADR 0007).
- Unknown, expired, and revoked keys must get the same `401` (api.md, criterion 9).
- Unknown request fields are rejected (ADR 0008).
- `libopenapi-validator` is used (ADR 0010).

Facts checked on 2026-11-02:

| Question | Result |
|---|---|
| What `libopenapi-validator` v0.16.2 reports | Each failure has `InstancePath` (already JSON-Pointer-escaped: key `a/b~c` arrives as `a~1b~0c`) and `KeywordLocation`. When one field fails, every other field is reported under `/unevaluatedProperties` as well (the ADR 0010 trap, confirmed on `PUT /v1/seller-profile`). A nullable field written as `oneOf [schema, null]` gives two failures, one of them "want null". `required` and `additionalProperties` failures sit on the parent object and name the properties only in the message text. `OriginalJsonSchemaError` is nil. |
| Cost of building the validator | About 170 ms for `openapi.yaml`, then under 1 ms per request |
| Vercel and files outside `apps/api` | The project's Root Directory is `apps/api`, and `go:embed` cannot reach files outside the module |
| Go's `ServeMux` and the route | `r.Pattern` holds the matched route (`PUT /v1/seller-profile`), the same path as the contract's minus `/v1` |
| Vercel's Go entry point | The preset looks for `main.go`, `cmd/api/main.go`, or `cmd/server/main.go` (ADR 0004), so a second command in `cmd/admin` is not deployed |

## Options

### Key encoding

**A. Base32, lowercase, no padding.** Prefix 10 random bytes (16 characters), secret 32 bytes (52 characters). Neither part contains `_` or `-`, so a strict regex splits the key unambiguously and a double-click selects all of it. Chosen by the owner.

**B. Base64url.** Shorter (43-character secret), but the alphabet contains `_`, the separator, so parsing would depend on the prefix's fixed length.

### When a response is sent

**A. Buffer the handler's response; commit, then send.** A failed commit becomes a `500`, never a success for a change that was rolled back. Costs memory for the response, which is a small JSON document.

**B. Let handlers write directly, commit afterwards.** No buffering, but a client could get `200` for a write that was lost at commit.

### Where the contract comes from at run time

**A. Copy `openapi/openapi.yaml` into `internal/api` with `go generate`, and embed it.** The binary carries the spec; the CI drift check fails if the copy is stale.

**B. Read the file from disk at start.** Depends on Vercel shipping files outside the Root Directory, which is a project setting, not a guarantee.

### Finding unknown fields

**A. Drop the library's `additionalProperties` and `unevaluatedProperties` failures, and find unknown fields by walking the body against the schema's declared properties** (through `allOf`, `oneOf`, `anyOf`, `items`, and `$ref`). Only objects whose schema is closed report them.

**B. Keep the library's reports and filter out declared names.** It needs the same schema walk, plus parsing property names out of message text for `additionalProperties`.

## Decision

- **Keys: Option A** (owner). `internal/apikey` generates, parses, and hashes keys; the admin command and the middleware share it.
- **Admin command `cmd/admin create-tenant`.** It runs as `postgres`, reading `DATABASE_URL` from the environment only, never a flag. One transaction creates the tenant, an empty seller profile, the first key, and two `audit_log` rows (`system`, no personal data). It prints the key once, alone on stdout, after the commit.
- **Authentication.**
  - A malformed key is rejected before any query.
  - An unknown prefix is still hashed and compared, against a random dummy hash. The comparison is `subtle.ConstantTimeCompare`.
  - Expired and revoked keys get the same `401` as unknown ones: same body apart from `request_id`, and `WWW-Authenticate: Bearer`.
  - A suspended tenant gets `403`.
  - `TouchAPIKey` runs inside the request's transaction, so it is rolled back with a failed request.
- **One transaction per request: Option A.** Authentication, the handler, and the commit share one `db.InTenantTx`. Below `400` it commits and then sends; at `400` or above it rolls back and then sends; a begin or commit failure sends a `500`. Database work has a 10-second deadline. This is also where idempotency keys (A3) will be stored, in the same transaction, in the next slice.
- **Validation after authentication.** A client without a valid key learns nothing about the request format, and the validator's cost is only paid for authenticated requests.
  - Wrong `Content-Type` gives `415`; a body that is not JSON gives `400`; a bad path or query parameter gives `400 bad_request`; schema failures give `422 validation_failed` with one `errors` entry per field.
  - The keyword maps to the field code: `type`, `format`, `pattern` → `invalid_format`; `enum` → `invalid_value`; `maxLength` → `too_long`; `required` → `required` at the missing property.
- **Contract at run time: Option A.** The validator is built once per process. `New` starts it in the background, so it overlaps a cold start instead of delaying the first request.
- **Unknown fields: Option A.** For a nullable `oneOf`, the "want null" branch loses to any other error at the same pointer.
- **Routing.** Only implemented operations are registered on the mux; the strict handler embeds a nil `StrictServerInterface` for the rest, which can never be reached. Unimplemented paths keep answering `404`.
- **No database URL** (owner): the server starts, `/healthz` works, and `/v1` answers `500` with "database not configured" in the log. The pool is created without connecting, so `/healthz` never waits for the database. A malformed URL stops the server with a fixed message, because pgx redacts passwords from parse errors only on a best-effort basis.

## Consequences

**Positive**

- A request either commits and reports success, or reports failure and changed nothing. Idempotency (next slice) can rely on that.
- The 401 cases have one test that compares them byte for byte. Each check (hash, expiry, revocation, suspension, rollback on error, commit before send) was broken on purpose and its test failed.
- The `unknown_field` false positive from ADR 0010 is fixed and tested, including next to an invalid field and on nested arrays (`/lines/0/bogus`).
- Timestamps are always returned in UTC, as the contract says, whatever the server's time zone.

**Negative, and how we handle it**

- **Unauthenticated requests with a well-formed key cost one database query each.** Rate limiting comes in phase 4; until then the pool's small size caps the load one instance puts on the database.
- **The unknown-field walk takes the union of properties across `oneOf` branches.** A property declared in only one branch is not reported when the body matches another. The library still rejects the body, and the fallback error makes it a `422`.
- **`required` property names are read from the library's message text**, because the structured error is not exposed. The names come from our own contract (snake_case only), and a test pins the format.
- **The spec is in the repository twice** (`openapi/openapi.yaml` and the generated copy in `apps/api/internal/api`). CI's drift check keeps them equal.
- **A response is held in memory until commit.** Responses are small. File downloads go through signed URLs (A8), never through the API.
- **The touch on `last_used_at` is lost when a request fails**, so the column means "last successful use".

## Follow-ups

- Set `JADE_API_DATABASE_URL` on the Vercel project `jade-invoicing-api`, with the owner's go-ahead.
- Idempotency keys (slice 7b): store the response from the buffer, in the same transaction.
- `cannot_revoke_current_key` needs the key ID in the request context; add it with `POST /v1/api-keys/{id}/revoke`.
- Rate limiting of failed authentication (phase 4).
- Use `admin.CreateTenant` in the store tests' fixture instead of its own insert, so test tenants match real ones (review finding, left for a later slice).
