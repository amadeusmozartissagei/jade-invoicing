# Public API and webhooks (phase 1)

- **Status:** Accepted (ADR 0008 for A1 to A8, ADR 0009 for W1 to W5). The contract is [`openapi/openapi.yaml`](../../openapi/openapi.yaml); error codes are in [`docs/api/errors.md`](../api/errors.md).
- **Date:** 2026-11-01
- **Sources:** [data-model.md](data-model.md) (ADR 0007), ADR 0003 (queue worker), ADR 0005 (routing, database access), and the standards listed under [Sources](#sources), checked on 2026-11-01.

This is a living design document, not an ADR. It fixes the contract between the Go API and its clients: conventions, errors, idempotency, concurrency control, pagination, the endpoint list, and webhook delivery. It is the input for the OpenAPI file, and once both are agreed the API and the dashboard can be built in parallel.

## Decisions for the owner

Each has a recommendation and a realistic alternative. The owner accepted the recommendation for all thirteen on 2026-11-01.

| # | Question | Recommended | Alternative |
|---|---|---|---|
| A1 | Spec-first with code generation? | Hand-written OpenAPI 3.1 is the source of truth. `oapi-codegen` (≥ v2.8.2) generates the Go types and a `net/http` server interface in strict mode; generated code is committed and CI fails if it is stale | Generate types only and write routing by hand; or no generation, with the spec checked only by contract tests |
| A2 | Error format | RFC 9457 problem details, with a stable `code` member, an `errors` list with JSON Pointers for validation failures, and a `type` URI that points to the error's section in `docs/api/errors.md` | `type` left as `about:blank` and only the `code` member (simpler, but the RFC then gives the response no meaning beyond its status code) |
| A3 | Idempotency keys | Required on every `POST`. Only successful responses are stored, in the same transaction as the change; a concurrent repeat is detected with a transaction-scoped advisory lock and gets `409` | Optional keys; or store error responses too, with an `in_progress` state and crash recovery |
| A4 | Lost updates on drafts | `ETag` from the invoice's `version`; `If-Match` required on `PATCH` and `DELETE` of a draft (`428` if missing, `412` if stale), optional on `issue` | Last write wins |
| A5 | How a draft is edited | `PATCH` with JSON Merge Patch (RFC 7396); `lines`, when present, replaces all lines | `PUT` of the whole draft; or line sub-resources (`POST /invoices/{id}/lines`) |
| A6 | Pagination | Cursor (keyset) on `(created_at, id)`, opaque cursor, `limit` 1 to 100 (default 25), no total count | `offset` and `limit` with a total |
| A7 | Money and quantities in JSON | Decimal strings (`"1190.00"`, `"2.5"`) | JSON numbers |
| A8 | How clients get the PDF | `GET /v1/invoices/{id}/files` returns metadata and a short-lived signed Storage URL per file | `GET .../pdf` that redirects (`302`) to the signed URL |
| W1 | Webhook signing | The Standard Webhooks scheme: `webhook-id`, `webhook-timestamp`, `webhook-signature: v1,<base64 HMAC-SHA256>` over `id.timestamp.body`, secret `whsec_<base64>` | A scheme of our own (for example Stripe-style `t=…,v1=…`) |
| W2 | What the payload contains | A thin snapshot: IDs, type, status, number, dates, totals. **No personal data**; the client fetches details through the API | The full invoice with lines and parties |
| W3 | Retry schedule | The Standard Webhooks example schedule (10 attempts over about 3 days), rounded up to the worker's 10 s tick, with jitter; then `dead` | A shorter schedule (for example 6 attempts over 1 day) |
| W4 | Secret rotation | In phase 1: rotating keeps the old secret for 24 h and signs with both | Later (phase 4) |
| W5 | Manual redelivery | In phase 1: `POST /v1/webhook-deliveries/{id}/redeliver` re-queues a `dead` delivery | Later (phase 3, with the dashboard) |

Why each recommendation is made is explained in the sections below.

## Conventions

- **Base path** `/v1`. Additive changes (new endpoints, new optional request fields, new response fields, new enum values, new event types) do not change the version. Clients must ignore unknown fields and handle unknown enum values; the spec says so in its description.
- **JSON only**, `application/json` in and out, except `PATCH` (`application/merge-patch+json`) and errors (`application/problem+json`). Field names are `snake_case`, matching the database columns.
- **Unknown request fields are rejected** (`422 validation_failed` with `unknown_field` and a pointer), so a typo such as `qty` or `due_dat` fails instead of a field being lost silently, which matters when the lost field is a due date or a buyer reference. This follows Zalando's API guideline 109 ("Unknown input fields in payload or URL should not be ignored"), which also says ignoring is not an option for `PUT` (the seller profile). Tolerance runs the other way for clients: they must ignore unknown fields in responses (guideline 108). The guideline suggests `400`; this API uses `422` because the JSON is well-formed, and so every field problem has the same shape. A client that sends back a whole `GET` response as a `PUT` body gets read-only fields such as `updated_at` rejected; the docs say to send only the input fields.
- **Authentication:** `Authorization: Bearer jade_sk_<prefix>_<secret>` on every `/v1` endpoint (key format in [data-model.md](data-model.md#api_keys-s-i-u)). A missing, malformed, unknown, expired, or revoked key gets the same `401` with `WWW-Authenticate: Bearer`, so the response does not tell an attacker which keys exist. A suspended tenant gets `403`.
- **IDs:** UUIDs, as stored. No type prefixes (`inv_…`), so the API and the database use the same value.
- **Money (A7):** amounts are strings with exactly the currency's minor units (`"1190.00"` for EUR, `"11900"` for JPY); quantities and unit prices are strings with up to 6 decimals. Many JSON parsers read numbers as binary floats, which cannot hold `0.10` exactly; a string survives every client. The server rejects amounts with more decimals than the currency allows, instead of rounding them silently. `currency` is returned next to every amount group.
- **Dates and times:** business dates as `YYYY-MM-DD`; timestamps as RFC 3339 in UTC (`2026-11-01T09:30:00Z`).
- **Request size:** bodies over 1 MiB get `413`. With at most 500 lines per invoice (data model) this is ample, and it stays far below Vercel's 4.5 MB limit.
- **Request ID:** every response has a `Request-Id` header, generated by the server (client values are not trusted), and every error body repeats it as `request_id`. It is the `request_id` in the audit log.
- **Status codes:** `200` read or update, `201` create (with `Location`), `204` delete, `400` malformed request, `401`, `403`, `404` (also for another tenant's IDs, which RLS hides anyway), `405`, `409` the current state does not allow the request, `412`, `413`, `415`, `422` the request content is wrong, `428`, `500`. Rate limiting (`429`) comes in phase 4.

## Errors (A2)

Every error is an RFC 9457 problem details object, `Content-Type: application/problem+json`:

```json
{
  "type": "https://github.com/amadeusmozartissagei/jade-invoicing/blob/main/docs/api/errors.md#invoice_not_draft",
  "title": "Invoice is not a draft",
  "status": 409,
  "detail": "Invoice 0b6c… is issued; only drafts can be edited. Issue a credit note or a corrected invoice instead.",
  "code": "invoice_not_draft",
  "request_id": "…"
}
```

- `type` is the problem type's identifier. RFC 9457 §3.1.1 says consumers "MUST use the type URI ... as the problem type's primary identifier", so it is frozen: a `type` value never changes within `/v1`.
- `type` is an absolute `https` URL to the error's section in `docs/api/errors.md`. The RFC recommends absolute URIs and encourages resolvable ones; it also allows URIs that do not resolve, so the identifier stays valid even if the link breaks. The project has no domain of its own, and the alternatives are worse: the API's own host differs between local, preview, and production, which would give one error several identifiers, and a `tag:` URI would need an email address or a domain the project owns. GitHub redirects a renamed repository's URLs, as long as the old name is never reused.
- `code` is the same identifier without the URL (`invoice_not_draft`), an extension member (RFC 9457 §3.2) for logs and `switch` statements. It always equals the fragment of `type`. Clients never branch on `title` or `detail`.
- Each type's title and status are listed in `docs/api/errors.md`, as RFC 9457 §4 requires for a new problem type.
- Validation failures (`422`, code `validation_failed`) add an `errors` list, one entry per field, in the shape of the RFC's own example:

  ```json
  "errors": [
    {"pointer": "/lines/0/quantity", "code": "must_be_positive", "detail": "quantity must be greater than 0"}
  ]
  ```

  `pointer` is a JSON Pointer (RFC 6901) into the request body.
- `500` responses carry only the generic title, `code: internal_error`, and the request ID. No stack traces, SQL, or constraint names (RFC 9457 §5); those go to the server log under the same request ID.
- Database constraint violations that a client can cause (a duplicate `external_ref`, a trigger rejecting a change to an issued invoice) are mapped to their own codes, not passed through as `500`.

The full list of codes, and the field-level codes inside `errors`, is in [`docs/api/errors.md`](../api/errors.md). The rule for choosing between the two common statuses follows RFC 9110: `422` when the request content is well-formed but wrong (§15.5.21 speaks of "the contained instructions"); `409` when the request is fine, or has no content, but conflicts with "the current state of the target resource" (§15.5.10). So `POST /issue` on an incomplete draft is `409 invoice_incomplete`, not `422`: the issue request has no content to be wrong, and the client fixes the draft and issues again, which is the situation §15.5.10 describes.

## Idempotency (A3)

Retrying a `POST` after a timeout must not create a second invoice, payment, or credit note. The behaviour follows the IETF draft `draft-ietf-httpapi-idempotency-key-header-08` (active Internet-Draft, not an RFC).

- **Which requests:** every `POST` under `/v1` requires `Idempotency-Key`. A missing key gets `400 idempotency_key_required`. One rule for all `POST`s is easier to document and to test than a per-endpoint list. `PATCH` and `DELETE` are protected by `If-Match` instead (A4).
- **The key:** 1 to 255 printable ASCII characters; a UUID v4 is recommended. The draft defines the header as a Structured Field string, which is quoted on the wire (`Idempotency-Key: "8e03978e-…"`); the server accepts that form and also a bare value, since many clients send one. Scoped to the tenant: two tenants can use the same key.
- **Fingerprint:** SHA-256 over the method, the path, and the raw body bytes. A repeat must send the same bytes; the same key with a different fingerprint gets `422 idempotency_key_reused`.
- **Lifetime:** 24 hours from the first successful request. A daily `pg_cron` job deletes expired rows.

How a request with a key runs, inside the one transaction that every tenant request already uses (ADR 0005):

1. `select pg_try_advisory_xact_lock(hashtextextended(tenant_id || ':' || key, 0))`. If another transaction holds it, the same key is being processed right now: answer `409 idempotency_key_in_use` with `Retry-After: 1`.
2. Look up `(tenant_id, key)` in `idempotency_keys`. If found and not expired: same fingerprint, replay the stored status and body with the header `Idempotent-Replayed: true`; different fingerprint, `422`.
3. Otherwise run the handler. On a `2xx`, insert the key row with the status and body, and commit everything together.
4. On a `4xx` or `5xx`, roll back. Nothing is stored, and the client may retry with the same key.

Why this design:

- **The stored response commits atomically with the change.** There is no `in_progress` row that a crash could leave behind, so no recovery job is needed. A crash before commit leaves neither the change nor the key.
- **Errors are not stored** because a failed request changed nothing; retrying it is safe and lets a client retry after fixing, for example, an incomplete seller profile. The draft defines the error responses for the key itself (400, 409, 422) and asks only for a response "consistent with the request being completed"; it does not say which results to store, so that is the server's choice.
- **The advisory lock** gives the draft's `409` for concurrent repeats without waiting. It is held only until the transaction ends, so it is safe behind the transaction pooler. Tested on 2026-11-01 through the Supabase transaction pooler with `pgx` exec mode: a second transaction got `false` while the first held the lock, and a third got `true` after the first committed. A hash collision between two different keys would only cause a false `409` that clears on retry.

**Secrets are never stored for replay.** Three responses carry a secret that is shown once: `POST /v1/api-keys`, `POST /v1/webhook-endpoints`, and `.../rotate-secret`. Storing their bodies would keep a plaintext API key or signing secret in the database for 24 hours. Their responses always carry `secret_redacted`: `false` in the first response, and in the stored copy the secret is replaced by `null` and `secret_redacted` is `true`. A client that lost the first response revokes the key (or rotates the secret) and creates a new one.

**Changes to the data model:** `idempotency_keys` drops `state` (no in-progress rows). `response_body` can contain personal data (a created customer), so it is marked *PII*; the 24-hour lifetime keeps that short.

## Concurrency control (A4)

Two people editing the same draft must not overwrite each other silently.

- `GET` of an invoice returns `ETag: "<version>"`. The `version` column already exists for this ([data-model.md](data-model.md#invoices-s-i-u-d-drafts-only)).
- `PATCH` and `DELETE` of a draft require `If-Match`. Missing: `428 precondition_required` (RFC 6585). Not matching the current version: `412 precondition_failed` (RFC 9110). Each successful edit increments `version` and returns the new `ETag`.
- `POST /issue` accepts `If-Match` and checks it when given, so a client can make sure it issues exactly the draft it last saw. It is optional because issue is already protected by its idempotency key.
- Issued documents do not change content, so they need no precondition.

## Editing drafts (A5)

`PATCH /v1/invoices/{id}` takes a JSON Merge Patch (RFC 7396): fields present are set, `null` clears an optional field, absent fields stay. `lines`, when present, replaces the whole list, because merge patch has no way to edit one element of an array. The server recalculates the totals and the tax breakdown on every change and returns the full draft.

Line sub-resources would allow editing one line at a time, but they add five endpoints and a version check per line. A draft has at most 500 lines, so sending them all is cheap.

## Pagination (A6)

List endpoints return:

```json
{ "data": [ … ], "next_cursor": "eyJ0IjoiMjAyNi0xMS0wMVQwOTozMDowMFoiLCJpIjoiMGI2YyJ9" }
```

- Sorted by `created_at` descending, then `id`. The cursor is the last row's `(created_at, id)`, base64url-encoded; clients treat it as opaque. `next_cursor` is `null` on the last page.
- `limit` 1 to 100, default 25. Filters are query parameters (`status`, `document_type`, `customer_id`, `created_after`, `created_before`), and a cursor is only valid with the filters it was issued for.
- Keyset pagination costs the same on page 1 and page 1,000, and rows inserted while paging do not shift the pages. Offset pagination re-reads all skipped rows and shows duplicates when rows are added. No total count, because counting is a full scan that grows with the tenant.
- **Limitation:** this is for browsing, not for syncing. A row whose transaction commits late can have a `created_at` earlier than rows already read, and be missed. Clients that need every change use webhooks (or the event feed planned for later, see [data-model.md](data-model.md#not-in-phase-1)).
- The list indexes in the data model gain `id` as their last column: `(tenant_id, status, created_at desc, id desc)`.

## Endpoints

Every endpoint below needs an API key. `POST`s need `Idempotency-Key`.

### Tenant and settings

| Method and path | Purpose |
|---|---|
| `GET /v1/tenant` | The tenant's jurisdiction, currency, time zone, Japanese rounding mode, fiscal year end |
| `GET /v1/seller-profile` | The seller details printed on invoices |
| `PUT /v1/seller-profile` | Replace them. Not checked for completeness here; that happens at issue |
| `GET /v1/number-series`, `POST /v1/number-series` | List and create series. Editing a series comes later |
| `GET /v1/api-keys` | List keys (never the secret) |
| `POST /v1/api-keys` | Create a key; the full key is in this response only |
| `POST /v1/api-keys/{id}/revoke` | Revoke. A tenant cannot revoke the key making the request, so it cannot lock itself out by mistake |

Tenants and their first API key are created by an admin script in phase 1 (data model). Changing jurisdiction or currency is not offered.

### Customers

| Method and path | Purpose |
|---|---|
| `POST /v1/customers` | Create. `external_ref` is unique per tenant (`409 conflict_external_ref`) |
| `GET /v1/customers` | List, filter by `external_ref` |
| `GET /v1/customers/{id}` | Read |
| `PATCH /v1/customers/{id}` | Merge patch. Issued invoices keep their own copy, so editing is safe |

No delete (data model): erasure arrives in phase 4.

### Invoices, credit notes, corrected invoices

One resource for all three document types (D3).

| Method and path | Purpose |
|---|---|
| `POST /v1/invoices` | Create a draft. `document_type` is `invoice`, `credit_note`, or `corrected_invoice`; the last two need `preceding_invoice_id` |
| `GET /v1/invoices` | List and filter |
| `GET /v1/invoices/{id}` | Read, with lines, tax breakdown, totals, status, and the derived payment state. This is the **status polling** endpoint |
| `PATCH /v1/invoices/{id}` | Edit a draft (A4, A5) |
| `DELETE /v1/invoices/{id}` | Delete a draft |
| `POST /v1/invoices/{id}/issue` | Issue: check completeness, assign the number, freeze. `409 invoice_incomplete` lists what is missing |
| `POST /v1/invoices/{id}/void` | Void an issued invoice or corrected invoice (D4). Body: `reason`. Returns the voided original and the full credit note issued with it |
| `GET /v1/invoices/{id}/files` | Files with signed download URLs (A8) |
| `POST /v1/invoices/{id}/payments` | Record a payment (D9) |
| `GET /v1/invoices/{id}/payments` | List payments |

`allowance_total`, `charge_total`, and `rounding_amount` are always `0` in phase 1 and cannot be set (D7). The invoice response adds two derived fields: `amount_paid` (sum of payments) and `payment_status` (`unpaid`, `partially_paid`, `paid`, or `null` for drafts and credit notes).

Correcting an invoice (D10) is a `POST /v1/invoices` with `document_type: corrected_invoice` and `preceding_invoice_id`, then `issue`. A convenience endpoint that copies the original's lines into a new correction draft can be added later without breaking anything.

**Files (A8).** A file entry has `kind`, `content_type`, `byte_size`, `sha256`, `url`, and `url_expires_at`. The URL is a Supabase Storage signed URL valid for a few minutes, so files never pass through a Vercel function (4.5 MB limit, ADR 0001) and a leaked URL soon stops working. A JSON response is easier to test and to document than a redirect, and it carries the hash. When the PDF is rendered (at issue or in a queue job) depends on the Typst spike (ADR 0006); the list is empty until a file exists, so the contract works either way.

### Webhook endpoints and deliveries

| Method and path | Purpose |
|---|---|
| `POST /v1/webhook-endpoints` | Register a URL; the signing secret is in this response only |
| `GET /v1/webhook-endpoints`, `GET /v1/webhook-endpoints/{id}` | List and read (never the secret) |
| `PATCH /v1/webhook-endpoints/{id}` | Change `url`, `event_types`, `enabled` |
| `POST /v1/webhook-endpoints/{id}/rotate-secret` | New secret, returned once; the old one keeps signing for 24 h (W4) |
| `GET /v1/webhook-endpoints/{id}/deliveries` | Recent deliveries with status and attempt count |
| `POST /v1/webhook-deliveries/{id}/redeliver` | Queue a `dead` delivery again (W5) |

No delete: deliveries reference the endpoint, and their history is useful. Disable with `enabled: false`.

### Not in the public spec

- `POST /internal/queue/drain`, called by `pg_cron` through `pg_net` (ADR 0003), authenticated with its own bearer token. It is not part of the client contract, so it is documented in `apps/api`, not in `openapi.yaml`.
- `GET /healthz` without authentication, for uptime checks and the Supabase keep-alive.

## Webhooks

### Events

`invoice.issued`, `credit_note.issued`, `invoice.voided`, `invoice.corrected`, `invoice.paid` (data model). A `corrected_invoice` emits `invoice.issued` for itself and `invoice.corrected` for the original. An endpoint with an empty `event_types` list receives all of them, including types added later.

### Signing (W1)

Deliveries follow the [Standard Webhooks](https://www.standardwebhooks.com/) specification:

```
POST <endpoint url>
content-type: application/json
webhook-id: <event id>
webhook-timestamp: 1793525400
webhook-signature: v1,K5oZfzN95Z9UVu1EsfQmfVNQhnkZ2pj9o9NDN/H/pI4=
```

- The signature is HMAC-SHA256, keyed with the endpoint's secret, over `webhook-id + "." + webhook-timestamp + "." + body`, base64-encoded, with the version prefix `v1,`. The body is signed as the exact bytes sent.
- The secret is `whsec_` followed by 32 random bytes, base64-encoded (the spec allows 24 to 64 bytes). It is stored encrypted (D8).
- During rotation (W4) the header carries two signatures separated by a space, one per secret; the receiver accepts the request if either matches.
- `webhook-id` is the event ID, the same on every retry, so the receiver can deduplicate on it. The timestamp lets the receiver reject old messages (replay protection). The spec leaves the tolerance to the implementer; our docs will recommend 5 minutes.

Why a published scheme instead of our own: clients can verify with existing open-source libraries (the spec repository ships them for Go, Python, JavaScript, Java, Ruby, Rust, PHP, C#, and Elixir, MIT-licensed), the signed content includes the ID and timestamp so a captured request cannot be replayed with a new timestamp, and the rotation story is already specified. Interview talking point: the platform follows a spec instead of inventing crypto formats.

### Payload (W2)

The body follows the spec's envelope:

```json
{
  "type": "invoice.issued",
  "timestamp": "2026-11-01T09:30:00Z",
  "data": {
    "object": "invoice",
    "id": "0b6c…",
    "document_type": "invoice",
    "status": "issued",
    "number": "INV-2026-000042",
    "customer_id": "7d1e…",
    "preceding_invoice_id": null,
    "issue_date": "2026-11-01",
    "currency": "EUR",
    "tax_inclusive_total": "1190.00",
    "amount_due": "1190.00",
    "amount_paid": "0.00"
  }
}
```

- **No personal data:** no names, addresses, contacts, or lines. The receiver calls `GET /v1/invoices/{id}` for details. This keeps the payload small (the spec suggests under about 20 KB), means a misconfigured endpoint leaks no personal data, and keeps `events.payload` free of personal data, so phase 4 erasure never has to rewrite events.
- **Snapshot at event time.** Deliveries are not ordered: a retried `invoice.issued` can arrive after `invoice.paid`. Receivers should use `timestamp` and `status`, or fetch the current state.

### Delivery and retries (W3)

The worker drains the queue every 10 s (ADR 0003). For each delivery:

- **Success** is any `2xx` within **15 s** (the lower end of the spec's 15 to 30 s recommendation; a receiver should accept the event and process it later). The response body is not read beyond a small limit.
- **Redirects are not followed** and count as failures, as the spec says.
- **`410 Gone`** disables the endpoint (`enabled = false`, with a reason) and stops its pending deliveries.
- **Anything else** (other status, timeout, connection error) is a failed attempt. The next attempt follows the spec's example schedule, measured from the previous attempt: 5 s, 5 min, 30 min, 2 h, 5 h, 10 h, 14 h, 20 h, 24 h. That is 10 attempts over about 75 hours, so a receiver that is down over a weekend still gets its events. Delays get ±10% jitter so that retries for many deliveries do not arrive at once, and a `Retry-After` header is honoured when it asks for a longer wait (capped at 24 h). Delays shorter than the worker's tick happen on the next tick.
- After the 10th failure the delivery becomes `dead`. It can be redelivered by hand (W5), which resets the schedule.
- The queue mechanics: on failure, `pgmq.set_vt` pushes the message's visibility out to the next attempt time; on success or `dead`, the message is archived. The delivery row and an attempt row are written in the same transaction (data model).

### Outbound request safety

The worker sends HTTP requests to URLs that tenants choose, which is a server-side request forgery risk: a URL could point at internal addresses.

- Only `https://` URLs on port 443, checked when the endpoint is saved.
- At connection time the dialer rejects loopback, private (RFC 1918, `fc00::/7`), link-local (including `169.254.169.254`), and unspecified addresses. Checking at connection time, not only when saving, also blocks DNS records that change after registration.
- No redirects (above), a fixed `User-Agent`, and the attempt log stores at most a short prefix of the response, never the request body.

## Changes to the data model

Applied to [data-model.md](data-model.md) on 2026-11-01:

- `idempotency_keys`: drop `state`; `response_body` marked *PII*.
- `webhook_endpoints`: add `previous_secret_ciphertext`, `previous_secret_key_id`, `previous_secret_expires_at` (W4), and `disabled_reason`.
- List indexes end in `id desc` (A6).

## Acceptance criteria

The OpenAPI file and its first implementation must meet these, each with a test:

1. `openapi.yaml` passes an OpenAPI 3.1 linter in CI, and every operation has its success and error responses, request examples, and the `Idempotency-Key` header where required.
2. Every error response in the tests is `application/problem+json` with `type`, `code`, and `request_id`; `code` equals the fragment of `type`, and every code the API can return has a section in `errors.md` (checked by a test that reads the file). A forced internal error leaks no SQL or stack trace.
3. A `POST` repeated with the same key and body returns the first response with `Idempotent-Replayed: true` and creates nothing new; with a different body it gets `422`; two concurrent requests with the same key create one resource and the other gets `409`; an issue that failed with `409 invoice_incomplete` succeeds with the same key once the draft is completed.
4. `PATCH` without `If-Match` gets `428`, with a stale one `412`; two concurrent edits with the same `ETag` leave exactly one applied.
5. Paging through a list of 250 invoices with `limit=100` returns each exactly once, in order, with `next_cursor: null` at the end.
6. Amounts with too many decimals for the currency are rejected with a pointer to the field; amounts sent as JSON numbers are rejected.
7. A test receiver verifies the webhook signature with an independent Standard Webhooks library (not our own code), including during secret rotation.
8. Retry timing follows the schedule (tested with a fake clock); `410` disables the endpoint; a redirect is a failure; endpoints resolving to private or loopback addresses are never contacted.
9. A key that is unknown, expired, or revoked gets the same `401` response, apart from `request_id`.
10. No row in `idempotency_keys` contains an API key or webhook secret; a replayed create returns `secret_redacted: true`.

## Code generation check (A1)

Run on 2026-11-01 against `openapi.yaml` with `oapi-codegen` v2.8.2 and Go 1.27, in a throwaway module:

| Check | Result |
|---|---|
| `redocly lint` (`@redocly/cli` 2.72.1, MIT, `recommended` rules) | Valid, no warnings (the local-server rule is turned off in `redocly.yaml`) |
| Generate `models` + `std-http-server` + `strict-server` | Generates; `go vet` passes |
| Error responses in strict mode | Each declared status gets its own typed response that writes `application/problem+json` with the `Request-Id` header, so handlers return problems without bypassing the generated code |
| Schemas used only by the `webhooks` section (`Event`, `EventInvoice`) | **Pruned by default**, which breaks compilation. Fixed with `output-options.skip-prune: true` |
| Merge patch: absent versus `null` | With plain pointers both decode to `nil`, which would make "clear this field" impossible. With `output-options.nullable-type: true`, nullable fields become `nullable.Nullable[T]` (`github.com/oapi-codegen/nullable`, Apache-2.0); a test showed `{}`, `{"note": null}`, and `{"note": "hello"}` decode to unspecified, null, and a value |

The configuration for the skeleton (step 4) is therefore:

```yaml
package: api
generate:
  models: true
  std-http-server: true
  strict-server: true
output-options:
  skip-prune: true
  nullable-type: true
```

## Open questions

- **Response and request validation:** settled in ADR 0010 (`libopenapi-validator`, MIT; spec checks structure, Go checks domain rules such as currency decimals). The request validation layer was built with the first phase 1 endpoints (ADR 0012).
- **PDF rendering time** depends on the Typst spike (ADR 0006); the files endpoint is shaped to work either way.

## Sources

Checked on 2026-11-01.

- **RFC 9457**, Problem Details for HTTP APIs: <https://www.rfc-editor.org/rfc/rfc9457.html>
- **draft-ietf-httpapi-idempotency-key-header-08** (22 October 2026, active Internet-Draft): <https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/>. Status codes used: missing key 400, reused key 422, concurrent 409.
- **Standard Webhooks** specification (headers, signature, secret format, rotation, payload envelope, example retry schedule, `410`, redirects, 15 to 30 s timeout): <https://github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md>. Reference libraries under `libraries/`, MIT license.
- **RFC 9110** (HTTP semantics: `ETag`, `If-Match`, `409` §15.5.10, `412`, `413`, `422` §15.5.21): <https://www.rfc-editor.org/rfc/rfc9110.html>, **RFC 6585** (`428`), **RFC 7396** (JSON Merge Patch), **RFC 6901** (JSON Pointer).
- **oapi-codegen**, Apache-2.0: README lists OpenAPI 3.0 and 3.1 support and the `std-http-server` generator (Go 1.24+); OpenAPI 3.1 arrived in v2.8.0 (17 July 2026), and v2.8.1 and v2.8.2 (24 and 31 October 2026) are security fixes for code generation, hence the minimum version: <https://github.com/oapi-codegen/oapi-codegen/releases>
- **Zalando RESTful API Guidelines**, rules 108 (clients tolerate unknown response fields, MUST) and 109 (servers do not ignore unknown input fields, SHOULD): <https://github.com/zalando/restful-api-guidelines/blob/main/chapters/compatibility.adoc>
- **GitHub, renaming a repository** (web traffic to the old URL is redirected unless the old name is reused): <https://docs.github.com/en/repositories/creating-and-managing-repositories/renaming-a-repository>
