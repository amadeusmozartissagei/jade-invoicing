# 0015. Invoice drafts and the tax engine

- **Status:** Proposed (implemented; waiting for the owner's review of the choices below)
- **Date:** 2026-11-21
- **Decided by:** The AI coding agent, inside the design Hamza Pratama (owner) already accepted in ADR 0007 (data model, D1 to D10) and ADR 0008 (API, A1 to A8). The choices listed here are the ones those ADRs left open. Each has the alternative that was considered, so the owner can change any of them.

## Context

Phase 1 slice (c) adds the first half of the invoice lifecycle:

- the tax engine
- the five draft operations of the contract: `POST /v1/invoices`, `GET` list, `GET` one, `PATCH` (merge patch with `If-Match`), and `DELETE`

Issuing, voiding, and numbering are slice (d).

The design is fixed in `docs/design/data-model.md` ("Tax calculation", "Invoice lifecycle") and `docs/design/api.md` (A4 to A7). The domain rules and their sources are in `docs/domain/germany-en16931-calculation.md` and `docs/domain/japan-qualified-invoice.md`:

- tax is computed once per rate per invoice, never per line
- DE rounds commercially to the cent [BMF-2026 Rn. 17]
- JP rounds to the yen by the issuer's choice of down, up, or half up [NTA-QA-57]

## Options and decisions

### C1. The tax engine is a pure package

**Chosen.** `apps/api/internal/invoicecalc`: one function, `Calculate(lines, params)`. It does no I/O and is used by both countries. Only the minor units and the rounding rule are parameters. All arithmetic uses `shopspring/decimal`:

- **Line net.** `DivRound` rounds the exact quotient half away from zero.
- **Tax.** It divides by 100 with `Shift(-2)`, which is exact. `Div` would round at 16 decimals before the tax rounding sees the value.
- **Size.** An amount over 16 integer digits returns `TooLargeError` with the line index. The API answers it as `422 out_of_range`.

The engine is tested in three ways:

- **Fixed cases**, all with exact amounts:
  - exact half cents and half yen
  - every JP rounding mode
  - several rates on one invoice
  - a case where per-line rounding would give a different tax (5 × €0.03 at 19% is €0.03, not €0.05)
- **An oracle test.** 2,000 random invoices are compared with an independent implementation in `math/big`. The test also checks the BR-CO formulas on every result.
- **Mutation checks.** Each rule was broken on purpose (rounding mode, line rounding, prepaid amount, size limit), and each change failed a test.

Alternative: compute in SQL. Rejected. Two implementations of one rule would drift, and the issue transaction needs the result in Go anyway.

### C2. Which date a draft's rates must be valid on

**Chosen.** In order:

1. `supply_date`
2. else `supply_period_end`
3. else `supply_period_start`
4. else today in the tenant's time zone

A rate that is not valid on that date is `422 rate_not_valid`. Issue (slice d) checks again against the final supply date, which it requires.

Alternative: accept any rate the jurisdiction has ever had while the draft has no date. That is more permissive, but a draft could then show totals at a rate that is not valid now.

### C3. Totals of a draft

**Chosen.** A draft stores its totals and VAT breakdown on every change, computed with the tenant's current rounding:

- DE: always `half_away_from_zero`
- JP: the tenant's `jp_tax_rounding`

`tax_rounding` stays null until issue, as the schema requires.

On every edit the breakdown rows are rewritten, not only when the lines change. The tenant's rounding mode may have changed since the draft was saved, and the stored breakdown must always add up to the stored `tax_total`.

### C4. Default buyer reference (BT-10)

**Chosen.** When `buyer_reference` is absent at create, it is set as follows:

1. the customer's `default_buyer_reference`
2. else `-`, **for DE only** [BMF-FAQ Q6]
3. a JP draft gets none, because the placeholder is a German rule and would only end up printed on a qualified invoice

An explicit `null` stays null.

The contract's field description was corrected to say "for DE (none for JP)". This is a documentation change; the schema is unchanged.

### C5. Credit note and corrected-invoice drafts

**Chosen.**

- **Which document they may refer to.** `preceding_invoice_id` must be an `issued` `invoice` or `corrected_invoice` of the tenant. Otherwise the answer is `409 invoice_not_issued`, as `errors.md` defines it. An unknown ID is `422 invalid_value`; RLS hides other tenants' invoices, so they count as unknown.
- **The customer.** It defaults to the preceding invoice's customer and must equal it (`must_match_preceding`), on create and on `PATCH`.
- **Rules left to issue (slice d).** The credit limit (`credit_exceeds_invoice`) and the correction rules (`correction_not_allowed`) are checked at issue. They depend on other documents that can change while this one is a draft.

### C6. `If-Match`

**Chosen.** The header is evaluated as RFC 9110 §13.1.1 describes:

- A comma-separated list of entity tags is accepted, and so is `*`.
- The comparison is strong, so a weak tag (`W/"1"`) never matches.

**Missing header.** `requireIfMatch` answers `428`, and it runs before request validation. Otherwise validation would report the missing required header as `400` (the same reason as idempotency in ADR 0014, Q1). Invoice `PATCH` and `DELETE` are routed through a `routeIfMatch` helper next to `route`, so the middleware order is written in one place.

**Order of the checks** for an existing document:

1. lock the row
2. compare the version: `412` if it differs
3. check the status: `409 invoice_not_draft` if it is not a draft

A test sends eight concurrent `PATCH`es made from the same `ETag`: exactly one applies and seven get `412`.

### C7. Writing up to 500 lines in one statement

**Chosen.** Lines and breakdown rows are sent as one JSON array and inserted with `jsonb_to_recordset`. Amounts are JSON strings, which Postgres casts to `numeric`, so no float is ever involved.

Two alternatives did not work:

- **`unnest` over twelve arrays.** `sqlc` cannot analyse a multi-argument `unnest`.
- **`COPY`.** It is not a safe assumption behind the transaction pooler.

Related gotcha: in pgx's exec mode a `[]uuid.UUID` parameter cannot be encoded, because pgx does not know the parameter's type. The child queries therefore take `text[]` and cast it to `uuid[]`.

### C8. Merge patch details

- **A patch that changes nothing** (including `lines` that are equal by value, such as `"2.0"` and `"2"`) writes nothing, keeps the version, and is not checked again.
- **Lines not in the body.** When the lines are not in the request and a new date makes their rate invalid, the error points at the date field (`/supply_date`), with the line number in `detail`. Pointers are defined as pointing into the request body.
- **Fields that cannot be cleared.** `null` for `customer_id`, `series_id`, `prepaid_amount`, or `lines` is rejected by the contract layer (`not_allowed`).

### C9. Audit and output format

**Audit.** The log records `invoice.create`, `invoice.update` (with the names of the changed fields only, as in ADR 0014), and `invoice.delete`.

**Output format.**

- Amounts are written with exactly the currency's minor units (`"950.00"`, `"2430"`).
- Quantities, prices, and rates are written in canonical form without trailing zeros: `"95.00"` is returned as `"95"`. These are values, not money, and the contract only bounds their decimals.

## Consequences

- The tax rule exists once, in Go, with an oracle test. The issue slice reuses it unchanged.
- **Payment state.** The invoice response's `amount_paid` and `payment_status` are already derived from `payments`, so the payments slice needs no change to this code.
- **Time zones.** `cmd/api` now embeds `time/tzdata`. The tenant's time zone decides "today", and the Vercel runtime may not ship zoneinfo.

**Follow-ups:**

- **Re-reading after writes (review finding kept).** Create and `PATCH` read the draft back with the same four child queries as `GET`, instead of building the response from memory. The cost is four extra round trips per write. It was kept so that a write response and a later `GET` are built by the same code from the same data; the GET test compares them.
- **Prepaid amount larger than the total.** This gives a negative `amount_due`. The schema allows it, and EN 16931 does not forbid it. Whether issue should reject it is an open question for slice (d).
- **Race detector.** `go test -race` cannot run on the owner's Windows machine (no C compiler). CI runs it.

## Acceptance criteria

Each has a test in `apps/api/internal/httpserver/invoices_test.go` (and the engine's in `internal/invoicecalc`). Every response is also checked against the contract.

1. **German draft.** A draft with two rates returns `201` with `Location` and `ETag: "1"`, the exact line amounts, the breakdown, and totals 989.90 / 183.29 / 1173.19. It is audited. A repeat with the same idempotency key replays it with its `ETag`. The buyer reference defaults as described in C4.
2. **Japanese draft.** It rounds with the tenant's mode, once per rate. After the tenant changes its mode, the next edit recomputes the tax and the breakdown.
3. **Domain rules.** Each rule the contract cannot express gives `422` with the right pointer and code, and nothing is written:
   - positive quantity and base quantity
   - rate validity by jurisdiction and date
   - periods
   - yen decimals
   - size limit
   - unknown, missing, or other-tenant references
   - document-type rules
4. **Credit note and corrected-invoice drafts.** They need an issued invoice or corrected invoice (`409 invoice_not_issued` otherwise), and they take its customer.
5. **`GET`.** It returns the same document as the create, with its `ETag`. Another tenant's ID and an unknown ID both get `404`.
6. **`PATCH`.**
   - `428` without `If-Match`; `412` for a stale tag, a weak tag, or an unquoted value.
   - The lines are replaced and the totals recomputed.
   - `null` clears a field.
   - A no-op patch keeps the version.
   - Non-nullable fields reject `null`.
   - A rate made invalid by a new date is reported at the date.
   - An issued document gets `409 invoice_not_draft`.
   - Concurrent edits made from one `ETag`: exactly one applies.
7. **`DELETE`.** `428` without `If-Match`, `412` for a stale one. Otherwise `204`, and the lines are deleted too. A second delete gets `404`; an issued document gets `409`.
8. **List.** 250 invoices with tied `created_at` come back in pages of 100, each exactly once, newest first. The filters work, and a cursor used with other filters gets `400`.
