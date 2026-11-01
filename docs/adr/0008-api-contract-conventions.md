# 0008. API contract: OpenAPI 3.1 first with generated Go, problem details, idempotency keys on every POST

- **Status:** Accepted
- **Date:** 2026-11-01
- **Decided by:** Hamza Pratama (owner), with research and the draft by the AI coding agent

## Context

Clients integrate through the REST API, and the dashboard is built against the same contract in a separate session (CLAUDE.md, "Parallel work"). The contract therefore has to be fixed before either side is built, and it has to cover the parts that are hard to change later: errors, retries, concurrent edits, pagination, and number formats. The full design is in [`docs/design/api.md`](../design/api.md); this ADR records decisions A1 to A8 from it.

Constraints and facts (checked 2026-11-01, sources in the design document):

- Clients retry after timeouts. An invoicing API must not issue an invoice or record a payment twice.
- The IETF Idempotency-Key header is an active draft (-08), not an RFC. It defines `400` for a missing key, `422` for a reused key, and `409` for a request still in progress, and leaves to the server which results to store.
- Money must stay exact end to end (ADR 0007, D1).
- `oapi-codegen` (Apache-2.0) supports OpenAPI 3.1 since v2.8.0 and generates for plain `net/http` (ADR 0005). v2.8.1 and v2.8.2 fix code-injection issues in generation.
- One transaction per tenant request already exists for RLS (ADR 0005), behind the Supabase transaction pooler.

## Options

The design document lists each decision with an alternative. The main ones:

- **A1 Contract:** generate only types, or generate nothing and rely on contract tests.
- **A2 Errors:** problem details with `type: about:blank` and only a `code` member.
- **A3 Idempotency:** optional keys, or storing error responses too with an `in_progress` state and crash recovery.
- **A4 Draft edits:** last write wins.
- **A5 Edit shape:** `PUT` of the whole draft, or line sub-resources.
- **A6 Pagination:** `offset` and `limit` with a total count.
- **A7 Money:** JSON numbers.
- **A8 Files:** a redirect to the signed URL.

## Decision

The owner chose the recommended option for all eight:

1. **A1:** `openapi/openapi.yaml` (OpenAPI 3.1) is written by hand and is the source of truth. `oapi-codegen` at v2.8.2 or later generates the Go types and a `std-http-server` interface in strict mode. Generated code is committed, and CI fails if it is stale.
2. **A2:** errors are RFC 9457 problem details. `type` is a frozen absolute URL to the error's section in [`docs/api/errors.md`](../api/errors.md) and is the identifier; `code` repeats it without the URL; `request_id` links to the logs; validation failures add an `errors` list with JSON Pointers. `422` is for wrong request content, `409` for requests the current state does not allow (RFC 9110). Unknown request fields are rejected. The owner left these three details to the agent, asking for the best-supported option; the reasons are in the design document.
3. **A3:** `Idempotency-Key` is required on every `POST`. Only `2xx` responses are stored, in the same transaction as the change; failed requests store nothing and can be retried with the same key. A concurrent repeat is detected with `pg_try_advisory_xact_lock` and gets `409`. Responses that contain a one-time secret are stored with the secret removed.
4. **A4:** drafts carry `ETag` from their `version`; `PATCH` and `DELETE` of a draft require `If-Match` (`428` if missing, `412` if stale).
5. **A5:** drafts are edited with JSON Merge Patch; `lines`, when present, replaces the whole list.
6. **A6:** keyset pagination on `(created_at, id)` with an opaque cursor, `limit` up to 100, no total count.
7. **A7:** amounts, quantities, and prices are decimal strings in JSON.
8. **A8:** `GET /v1/invoices/{id}/files` returns file metadata with short-lived signed Storage URLs.

The main reason across all eight: each choice makes a failure visible instead of silent (a duplicate request, a lost edit, a rounded amount, a skipped row), and each one follows a published standard where one exists.

## Consequences

**Positive**

- The dashboard and the API can be built in parallel against one file, and the Go compiler checks that handlers match it.
- Retried requests are safe, and the stored response commits atomically with the change, so no recovery job is needed for half-finished keys.
- Errors are machine-readable and never leak SQL or stack traces.

**Negative, and how we handle it**

- **Generated code** is one more build step and must be kept in sync. CI checks it. A generation test on 2026-11-01 ([details](../design/api.md#code-generation-check-a1)) showed that strict mode gives every declared error status a typed `application/problem+json` response, and found two settings the project needs: `skip-prune` (schemas used only by `webhooks` were dropped otherwise) and `nullable-type` (without it, merge patch cannot tell an absent field from `null`). The latter adds `github.com/oapi-codegen/nullable` (Apache-2.0).
- **The generated code does not validate** patterns, lengths, unknown fields, or currency decimals. A validation layer is chosen in step 4.
- **Every `POST` needs a key**, which is more work for a client trying the API with `curl`. The docs show how to send one.
- **Holding an advisory lock per request** costs one query. A hash collision between two keys gives a false `409` that clears on retry.
- **Errors are not replayed**, so a repeated request can succeed where the first failed if the state changed in between. That is intended: a failed request changed nothing.
- **Keyset pagination has no page numbers or total**, and a row committed late can be skipped while paging; lists are for browsing, and webhooks are for keeping in sync.
- **Error `type` URIs point to GitHub**, because the project has no domain. They are identifiers first (RFC 9457 makes `type` the primary identifier), so they are frozen for `/v1` even if the link stops resolving; GitHub redirects a renamed repository as long as the old name is not reused.
- **The Idempotency-Key header is still a draft**; if the RFC changes status codes, a new API version follows it.

## Follow-ups

- Choose a Go library that validates real responses against an OpenAPI 3.1 document in tests, and record its license (step 4).
- Pin `oapi-codegen` in the skeleton and add the staleness check to CI.
- A daily `pg_cron` job deletes expired idempotency keys.
