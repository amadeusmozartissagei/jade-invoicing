# 0014. Customers CRUD, and how idempotency keys are stored, replayed, and expired

- **Status:** Accepted
- **Date:** 2026-11-10
- **Decided by:** Hamza Pratama (owner) accepted the slice's spec, its 15 acceptance criteria, and the agent's recommendation on all seven open questions (Q1 to Q7 below). The AI coding agent made the smaller implementation choices listed under "Agent's choices", recorded here for his review.

## Context

Phase 1 slice (b) adds the four customer operations of the contract (`POST`, `GET` list, `GET` one, `PATCH`) and the first `POST`, so idempotency keys (ADR 0008, A3) had to be built now. The design in `docs/design/api.md` fixes the main mechanism:

- keys scoped to the tenant
- a SHA-256 fingerprint of method, path, and body
- a transaction-scoped advisory lock for concurrent repeats
- only `2xx` responses stored, in the same transaction as the change
- a 24-hour lifetime

ADR 0012 provides the transaction this runs in: one transaction per request, with the response buffered until commit.

Building it raised questions the design left open:

- where in the middleware chain the key is handled
- what a replay must return besides status and body
- how expired rows go away
- how customer personal data is handled before phase 4
- whether customer changes are audited

## Options and decisions

### Q1. Where idempotency runs

**A (chosen).** A generic middleware for every `POST`, between authentication and request validation:

`authenticate → idempotent → validateRequests → handler`

A missing key must get its own code, `idempotency_key_required`. If validation ran first, it would report the missing required header as a plain `400 bad_request`. A replayed request was valid when it was first sent, so it does not need validating again.

B. Validate first, then idempotency. Gives the wrong code for a missing key.

### Q2. What a replay returns

**A (chosen).** A new column `idempotency_keys.response_headers` (`jsonb`) stores an allowlist of headers: `Content-Type`, `Location`, and `ETag` (for invoices later). `Request-Id` is never stored, because each request gets its own.

B. Rebuild headers per operation from the stored body. Each new endpoint would need its own rebuild code, which is easy to forget.

### Q3. Lifetime and cleanup

**A (chosen).** A key's row is valid for 24 hours. An expired row with the same key is deleted inside the request's transaction, and the request runs as new. A daily `pg_cron` job (`jade-delete-expired-idempotency-keys`, 03:41 UTC) calls `app.delete_expired_idempotency_keys()`. The function runs as `postgres` and cannot be executed by `jade_api`. Stored bodies can contain personal data (a created customer), so they should not outlive the 24 hours the contract promises.

B. Lazy deletion only. Rows for keys never reused would stay forever.

### Q4. Fingerprint, concurrency, errors

As designed in `api.md`:

- **Fingerprint mismatch.** The same key with a different method, path, or body gets `422 idempotency_key_reused`.
- **Concurrent repeat.** `pg_try_advisory_xact_lock(hashtextextended(tenant || ':' || key, 0))` gives `409 idempotency_key_in_use` with `Retry-After: 1`, without waiting.
- **Errors are not stored.** A failed request changed nothing, so the client can fix it and retry with the same key.

**Two details added:**

- **Length-prefixed fingerprint.** Each part (method, path, body) is length-prefixed before hashing, so moving bytes between the path and the body changes the hash.
- **Two key forms, one key.** The quoted Structured Field form (with `\"` and `\\` escapes) and a bare value name the same key.

### Q5. Personal data now versus phase 4

**Decision:** customer columns stay plain text (the data model marks them *PII*); field-level encryption comes in phase 4. In this slice:

- Request bodies and field values are never logged. A test captures the server log during a create, a `409`, a `422`, and a list, and checks that none of the values appear.
- The stored replay body holds personal data for at most 24 hours (Q3).
- The only list filter is `external_ref`, which is not personal data, so encrypting personal columns later will not break a query.

### Q6. Audit log for customers

**A (chosen).** `customer.create` and `customer.update` are written to `audit_log` in the same transaction. The actor is the API key, and `details` holds only the names of changed fields (`{"fields": ["name", "city"]}`), never their values. The data model's phase 1 list did not include customers. Auditing who changed personal data costs little and supports the phase 4 GDPR story.

B. Follow the data model as written and do not audit customers.

### Q7. Concurrency control on customer `PATCH`

**A (chosen).** No `If-Match`, as the contract says: last write wins. Issued invoices keep their own copy of the customer, so a later edit changes no issued document.

B. Add a `version` column and an `ETag`. That needs a contract change and a migration, for little benefit.

## Agent's choices

- **PATCH flow.** `PATCH` reads the row `for update`, merges in Go, and writes the whole row. A patch that changes nothing (`{}` or the same values) writes nothing, so `updated_at` and the audit log move only on a real change.
- **`null` on a non-nullable field.** `null` for `kind` or `name` gives `422` with field code `not_allowed`, as `errors.md` defines. This is general in `reqvalidate`: a type failure on a JSON `null` is always a non-nullable field, because nullable fields accept `null`.
- **Cursor format.** The cursor is base64url JSON with the last row's `created_at` (microsecond precision), its `id`, and a short hash of the filters. A cursor that is malformed or was issued for other filters gets `400 bad_request`. The query fetches one row more than the page size to know whether there is a next page.
- **Unique `external_ref`.** A duplicate is mapped from the unique constraint (`customers_tenant_id_external_ref_key`, SQLSTATE `23505`) to `409 conflict_external_ref`, on both create and patch.
- **Contract fix.** The contract lacked `400` on six `{id}` operations, though a malformed UUID always gets `400` (`getCustomer`, `getInvoice`, `deleteInvoice`, `listInvoiceFiles`, `listPayments`, `getWebhookEndpoint`). The responses were added, which is an additive change.
- **Recording key use without waiting (from the review).** `TouchAPIKey` now updates through `select … for update skip locked`. Before, the first request of a key in each minute locked the key's row until it committed. Every other request of that key waited behind it, so a repeated `Idempotency-Key` queued instead of getting an immediate `409`. If the row is busy, the touch is skipped; another request is recording the use anyway.
- **Same body checks on every path (from the review).** The idempotency middleware and validation share `readBody` (`Content-Type`, 1 MiB limit, valid JSON). A stored key therefore never turns a `415` into a replayed `201`.

## Consequences

**Positive**

- Every later `POST` (invoices, payments, API keys, webhook endpoints) gets idempotency by being routed, with no per-handler code.
- A crash before commit leaves neither the change nor the key, and no recovery job is needed.
- Each of the 15 acceptance criteria has a test, run as `jade_api` under RLS, with responses checked against the contract.
- Mutation-checked: each of these was broken on purpose, and its test failed:
  - the advisory lock
  - the fingerprint compare
  - storing only `2xx`
  - expiry
  - the replayed headers
  - the cursor's filter check and the cursor itself
  - the no-op patch
  - field names only in the audit log
  - `not_allowed`
  - the non-waiting key touch

**Negative, and how we handle it**

- **Hash collisions on the lock.** Two different keys can share an advisory-lock hash. The result is a false `409` that clears on retry.
- **Stored bodies hold personal data.** They are kept for up to 24 hours plus the time until the next daily cleanup. Phase 4 encryption must cover `idempotency_keys.response_body` too.
- **Keyset pagination is for browsing, not syncing** (`api.md`). A row whose transaction commits late can be missed.
- **Responses are buffered twice on `POST`** (the idempotency buffer inside the request buffer). The cost is small for JSON responses, and it keeps the middleware self-contained (review finding, left as is).
- **The concurrency test may not see a `409`.** With 10 parallel repeats it can observe only replays, so the `409` path is covered deterministically by a separate test that holds the lock from another transaction (review finding, left as is).

## Follow-ups

- **Redact secrets in stored replays.** One-time secrets must be removed from the stored body (`secret_redacted`) when `POST /v1/api-keys` and the webhook endpoints arrive; the middleware needs a per-operation hook for it.
- **Check the cleanup job on the remote project.** After `supabase db push`, check that the `pg_cron` job exists there.
