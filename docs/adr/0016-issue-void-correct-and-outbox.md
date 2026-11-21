# 0016. Issuing, voiding, and correcting documents, and the event outbox

- **Status:** Proposed (implemented; waiting for the owner's review of the choices below)
- **Date:** 2026-11-21
- **Decided by:** The AI coding agent, inside the design Hamza Pratama (owner) accepted in ADR 0007 (D4 void, D5 party copies, D6 numbering, D10 corrections), ADR 0008 (API), and ADR 0009 (webhooks). The choices here are the ones those ADRs left open. Each has the alternative that was considered.

## Context

Phase 1 slice (d) adds the two operations that give documents legal effect:

- `POST /v1/invoices/{id}/issue`, for invoices, credit notes, and corrected invoices
- `POST /v1/invoices/{id}/void`

Both write the transactional outbox (events, webhook deliveries, `pgmq` messages) that the webhook worker of slice (f) will drain. Drafts and the tax engine come from ADR 0015. The schema's triggers already enforce immutability and the state transitions (ADR 0011), so the Go code cannot break them even with a bug.

## Options and decisions

### I1. The issue transaction

**Chosen.** Everything runs in the request's one transaction (ADR 0012), in this order:

1. Read the draft.
2. Lock it and the document it refers to, in id order (`LockInvoices`).
3. Check `If-Match`, the status, and the preceding document.
4. Check completeness.
5. Recompute the totals and breakdown.
6. Copy the parties.
7. Take the number (`TakeNextNumber` locks the series row).
8. Mark the draft issued.
9. Write the events and the audit entries.

Any failure rolls back everything, the counter included, so a failed issue uses no number. A test issues 20 drafts concurrently and gets exactly 1 to 20.

This differs from `data-model.md` in one place: the number is taken **after** the computation, not before. Issues on one series queue on the series row lock, so taking it late keeps that lock short. The lock order is unchanged: invoice rows first, then the series. `data-model.md` was updated to match.

### I2. What a document needs before it can be issued

**Chosen.** Only the legally required items. Everything missing is listed at once in `409 invoice_incomplete`, with pointers into the draft (`/supply_date`, `/lines`), the seller profile (`/seller_profile/…`), or the customer (`/customer/…`).

**DE**, § 14 Abs. 4 UStG [UStG-14]:

- Nr. 1: the full name and address of both parties: legal name, street, city, post code, and country.
- Nr. 2: the seller's VAT ID or tax number. An empty string counts as missing.
- Nr. 6: a supply date, or a supply period with start and end. A period is given by both ends (`docs/domain/germany-en16931-mapping.md`).

**JP**, the qualified invoice's required items [NTA-EN]:

- the issuer's name and registration number
- the transaction date
- the recipient's name, which a customer always has

**Both:**

- at least one line
- every rate valid on the final supply date (`rate_not_valid` at `/lines/n/tax_rate`)

Alternative: also require the XRechnung-only fields now (seller contact, IBAN, buyer reference; BR-DE-1 to 7). Deferred to phase 2, where the XML is generated. They are not VAT requirements, and requiring them now would block every Japanese-style test tenant for no reason.

### I3. Dates and retention

- **Issue date.** Today in the tenant's time zone.
- **Retention.** `retention_until` follows `data-model.md` ("Retention"):
  - **DE** [UStG-14b]: 31 December of the issue year + 8.
  - **JP** [NTA-LEAFLET p.11]: the taxable period ends with the tenant's `fiscal_year_end_month`. Retention runs until the last day of the second month after that end, + 7 years. That last day is computed in the target year, so February 29 is handled; a table test covers it.

### I4. Credit notes and corrections at issue

**Credit notes.**

- **What they may refer to.** Only an issued `invoice` or `corrected_invoice`. Otherwise the answer is `409 invoice_not_issued`.
- **The limit.** The issued credit notes of one invoice, including this one, may not exceed its `tax_inclusive_total` (`409 credit_exceeds_invoice`). The original is locked, so two credit notes cannot both pass the check.

**Corrections.**

- Issuing one moves the original to `corrected` in the same transaction. It emits `invoice.issued` for the correction and `invoice.corrected` for the original.
- A second correction of the same original gets `409 correction_not_allowed`.
- As `data-model.md` says for phase 1, so does correcting an invoice that has payments or credit notes. A corrected invoice can itself be corrected.

### I5. What void creates

**Chosen.** Void issues a credit note that mirrors the original:

- same customer, number series, dates, buyer and order reference, and lines
- the note `Voids invoice <number>: <reason>`
- the **original's** tax rounding, not the tenant's current mode, so the credit cancels the invoice to the yen even after the tenant changed its setting (tested)
- the **original's parties**, copied from `invoice_parties` and not from today's profile and customer, so a void never fails because the seller profile or the customer was edited since (tested)

Then it marks the original `voided`. It emits `credit_note.issued` and `invoice.voided`.

**Rules.**

- **Existing credit notes.** An invoice that already has credit notes cannot be voided: the full credit note would exceed the total, and the issue rule from I4 answers `credit_exceeds_invoice`. The client credits the remaining amount instead. An earlier, separate check gave the same answer; the mutation test showed it was redundant, so it was removed.
- **Payments.** They do not block a void. Refunds are not in phase 1, and the credit note documents the cancellation either way.

Alternative: void as a status flag only. Already rejected in ADR 0007, D4.

### I6. The outbox

**Chosen.** Each event is one `app.events` row. Its `payload` is the exact Standard Webhooks envelope that will be sent (ADR 0009, W2):

- `type`
- `timestamp`
- `data`, the `EventInvoice` snapshot. It has no names, addresses, or lines; a test checks the exact keys.

**Fan-out.** One statement inserts a `webhook_deliveries` row for every enabled endpoint subscribed to the event type, where an empty list means all types. For each delivery it calls `pgmq.send` with a message that holds only `delivery_id` and `tenant_id`, because the queue tables have no RLS. A test checks:

- only subscribed, enabled endpoints get a delivery
- the message has exactly those two keys
- a failed issue leaves no event and no message

**Why that statement is hand-written.** `sqlc` does not know `pgmq`'s functions, so this one query is not generated. `pgmq.send` returns a set, so it is called through `cross join lateral`.

Alternative: a SQL function in a migration, so `sqlc` could call it. Not chosen: it adds a migration that has to be pushed to the remote project, for a single statement.

### I7. A `POST` without a body

`issue` has no request body in the contract. The body checks (Content-Type, JSON) are now skipped only when the contract declares no body for the operation (`reqvalidate.HasRequestBody`). An empty body is then accepted whatever `Content-Type` the client sends; many HTTP clients send `application/json` on every `POST`. An operation that has a body still answers `400` for an empty one, as before.

### I8. Audit

The log records:

- `invoice.issue` for every document type
- `invoice.correct` on the original
- `invoice.void` on the original
- `invoice.create` for the credit note that void creates

As always, the audit log holds no personal data.

## Consequences

- **What is now complete.** The invoice lifecycle of the data model works end to end: draft, then issued, then voided or corrected, with credit notes. Every rule the database cannot express has a test. Of 20 mutations, 19 failed a test; the one that did not exposed a redundant check, which was removed.
- **What the webhook slice (f) can rely on.** Events and their queued deliveries exist for every issue and void. The worker only has to read the queue.

**Follow-ups:**

- **Duplicate computation in void (review finding kept).** Void computes the credit note's line amounts to write its lines, and the issue step computes the full result again. Line amounts do not depend on the tax rounding, so the two always agree.
- **`invoice.paid`.** It comes with payments (slice after (f)).
- **Prepaid amount larger than the total.** Still open from ADR 0015: it gives a negative amount due.
- **Race detector.** `-race` runs only in CI (no C compiler on the owner's machine).

## Acceptance criteria

Each has a test in `apps/api/internal/httpserver/issue_test.go`. Every response is checked against the contract.

1. **Issue a German invoice.** The response has the number, the issue date in the tenant's time zone, the recorded rounding, the retention date, and both parties. Later edits to the seller or the customer do not change the parties. The `invoice.issued` event has only the snapshot keys, and the issue is audited. A replay with the same key returns the same document and uses no number.
2. **Incomplete documents.** Everything missing is listed in one `409 invoice_incomplete`, with each jurisdiction's own rules. A retry with the same idempotency key succeeds once the data is complete, and the failed attempt used no number.
3. **Numbering.** 20 concurrent issues on one series get exactly 1 to 20. A series with a prefix pads the counter (`INV-2026-0001`).
4. **Japan.** The tenant's rounding is recorded. Retention follows the fiscal year (and a table test covers DE, JP, leap years, and period boundaries).
5. **Issue preconditions.**
   - `If-Match` is optional; a stale one gets `412`.
   - An unknown document gets `404`.
   - A rate that is invalid on the final supply date is reported in `invoice_incomplete`.
   - An empty VAT ID counts as missing.
   - An empty body with a JSON `Content-Type` is accepted.
   - Issuing twice gets `invoice_not_draft`.
6. **Credit notes.** They may credit up to the invoice total exactly, and not one cent more.
7. **Corrections.** The original moves to `corrected`, both events are written, and a second correction is refused. A correction of a correction works. An invoice with a credit note or a payment cannot be corrected.
8. **Void.** It returns the voided original and a mirror credit note, which carries the original's parties and rounding. Void is refused for a voided invoice, a credit note, a draft, and an invoice with credit notes; an unknown document gets `404`.
9. **Outbox.** Deliveries and queue messages are written only for enabled, subscribed endpoints, with IDs only, and nothing is written when the issue fails.
