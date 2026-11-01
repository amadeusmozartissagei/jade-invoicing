# 0009. Webhooks: Standard Webhooks signing, thin payloads without personal data, three-day retries

- **Status:** Accepted
- **Date:** 2026-11-01
- **Decided by:** Hamza Pratama (owner), with research and the draft by the AI coding agent

## Context

Clients learn about issued, voided, corrected, and paid invoices through webhooks. Events are written to a transactional outbox and delivered by a Go worker that drains `pgmq` every 10 s (ADR 0003, ADR 0007). Delivery is at least once. The design is in [`docs/design/api.md`](../design/api.md#webhooks); this ADR records decisions W1 to W5.

Constraints:

- Receivers must be able to check that a request came from the platform and is not a replay.
- Payloads leave the platform's control, so whatever personal data they contain is out of reach of later erasure (phase 4).
- Retries run on the free tier: the worker ticks every 10 s and must stay within Vercel Hobby's invocation and CPU quotas (ADR 0001, ADR 0003).
- Tenants choose the URLs the worker calls, which is a server-side request forgery risk.

The [Standard Webhooks](https://github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md) specification (checked 2026-11-01) defines headers, an HMAC-SHA256 signature over `id.timestamp.body`, a `whsec_` secret format, rotation with several signatures, a `{type, timestamp, data}` envelope, an example retry schedule, and how to treat `410` and redirects. Its repository ships MIT-licensed verification libraries in nine languages, Go included.

## Options

- **W1 Signing:** our own scheme (for example Stripe-style `t=…,v1=…`).
- **W2 Payload:** the full invoice, with lines and parties.
- **W3 Retries:** a shorter schedule, about 6 attempts over a day.
- **W4 Secret rotation:** later, in phase 4.
- **W5 Manual redelivery:** later, with the dashboard.

## Decision

The owner chose the recommended option for all five:

1. **W1:** sign with Standard Webhooks `v1` (HMAC-SHA256). `webhook-id` is the event ID; secrets are `whsec_` plus 32 random bytes, stored encrypted (ADR 0007, D8).
2. **W2:** the payload is a thin snapshot (IDs, type, status, number, dates, totals) with **no personal data**. Receivers fetch details through the API.
3. **W3:** the spec's example schedule (immediately, then 5 s, 5 min, 30 min, 2 h, 5 h, 10 h, 14 h, 20 h, 24 h: 10 attempts over about 75 hours), ±10% jitter, `Retry-After` honoured up to 24 h, 15 s timeout, only `2xx` is success, no redirects, `410` disables the endpoint. After the last attempt the delivery is `dead`.
4. **W4:** rotating a secret keeps the old one for 24 hours, and deliveries carry both signatures.
5. **W5:** `POST /v1/webhook-deliveries/{id}/redeliver` re-queues a `dead` delivery.

Outbound requests go only to `https://` on port 443, and the dialer refuses loopback, private, link-local, and unspecified addresses at connection time.

## Consequences

**Positive**

- Clients verify with existing libraries; our tests do the same, so a mistake in our signing code fails a test written against someone else's implementation.
- No personal data in payloads or in `events.payload`, so erasure never has to chase webhook copies or rewrite events.
- A receiver that is down over a weekend still gets its events.

**Negative, and how we handle it**

- **Receivers make an extra API call** to get details. This also gives them the current state, which matters because deliveries are not ordered.
- **Long retry tails** keep messages in `pgmq` for up to three days and add worker work for endpoints that are gone for good. `410` lets a receiver stop them; auto-disabling endpoints after repeated `dead` deliveries is a later option.
- **A 15 s timeout per attempt** keeps a drain call short but can fail slow receivers; the docs tell receivers to acknowledge first and process later, as the spec advises.
- **Rotation adds three columns** to `webhook_endpoints` and a second HMAC per delivery for 24 hours.
- **The SSRF dialer** has to be tested with addresses that resolve to private ranges, including IPv6.

## Follow-ups

- Measure Active CPU per drain call with real deliveries before the first public demo (ADR 0003).
- Write the receiver docs: verification with the official libraries, a 5-minute timestamp tolerance, deduplication on `webhook-id`.
