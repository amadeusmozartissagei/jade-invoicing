# 0007. Data model: invariants enforced in Postgres (tenant isolation, money, immutability, numbering)

- **Status:** Accepted
- **Date:** 2026-11-01
- **Decided by:** Hamza Pratama (owner), with research and the draft by the AI coding agent

## Context

Phase 1 needs a schema for tenants, API keys, customers, invoices and their corrections, payments, events, webhooks, and an audit log. The full design is in [`docs/design/data-model.md`](../design/data-model.md); this ADR records the choices in it that are hard to change later.

Constraints:

- Tenants share one database. A bug in the Go code must not be able to show one tenant's data to another.
- Issued invoices are immutable in both countries, and corrections are new documents that refer to the original. German invoice numbers must be sequential and unique. (See [`docs/domain/README.md`](../domain/README.md).)
- Tax is calculated once per rate on the per-rate total in both countries, with different currencies (EUR with 2 decimals, JPY with none) and rounding rules. ([germany-en16931-calculation.md](../domain/germany-en16931-calculation.md))
- The API reaches Postgres through the Supabase transaction pooler and sets the tenant per transaction (ADR 0005). Background work runs from `pgmq` in the same database (ADR 0003).

## Options

The design document lists ten questions (D1 to D10), each with a recommended option and an alternative. The main alternatives were:

- **Money:** `bigint` minor units instead of `numeric`. Common in payment systems, but EN 16931 allows quantities and unit prices with more decimals, so a decimal type would be needed anyway, giving two representations.
- **Access:** tables in `public`, with the API connecting as `postgres`. Simpler, but `postgres` owns the tables, so RLS would not apply and isolation would rest on Go code alone.
- **Documents:** one table per document type instead of one `invoices` table.
- **Void:** a status flag instead of a credit note.
- **Snapshots:** `jsonb` instead of typed columns.
- **Numbering:** a Postgres `sequence`, which is not transactional (rollbacks leave gaps) and not per tenant.
- **Webhook secrets:** Supabase Vault, or plaintext until phase 4.
- **Payments:** a `paid_at` column on the invoice.
- **Corrections:** defer corrected invoices to a later phase, or offer them for Japan only.

## Decision

The owner chose the recommended option for all ten. D10 was added after the first nine were decided, once the NTA's Q&A had been read:

1. **D1 Money:** `numeric` everywhere (`numeric(18,2)` for amounts, `numeric(18,6)` for quantity and unit price), checked against the currency's minor units, and a decimal library in Go. Never a float.
2. **D2 Isolation:** a dedicated schema `app`. The API and the worker connect as `jade_api`, which has `NOBYPASSRLS` and owns nothing. RLS is enabled and forced on every table, with one policy shape: `tenant_id = app.current_tenant_id()`. No tenant set means no rows. API keys are looked up by a narrow `security definer` function. Foreign keys include `tenant_id`, so a cross-tenant reference fails in the database.
3. **D3 Documents:** one `invoices` table with `document_type` (`invoice` 380, `credit_note` 381, `corrected_invoice` 384) and a reference to the preceding invoice.
4. **D4 Void:** voiding issues a full credit note that refers to the original and marks the original `voided`, in one transaction.
5. **D5 Snapshots:** at issue, the seller's and buyer's details are copied into `invoice_parties`, one typed column per EN 16931 field.
6. **D6 Numbering:** the number is assigned at issue from a per-series counter row, updated and locked inside the issue transaction. A rollback undoes the increment, so there are no gaps or duplicates. Lock order is fixed: invoices first, then the series.
7. **D7 Scope:** phase 1 leaves out allowances and charges, VAT categories other than standard-rated, negative lines, gross prices, and more than one jurisdiction per tenant. The columns that later need them exist where cheap (the allowance and charge totals stay 0).
8. **D8 Webhook secrets:** encrypted in Go with AES-256-GCM; the key comes from the environment, and a key ID is stored for rotation.
9. **D9 Payments:** an insert-only `payments` table; "paid" is derived from it.
10. **D10 Corrections:** corrected invoices are in phase 1 for both countries. A `corrected_invoice` restates all items and refers to the original; issuing it moves the original from `issued` to `corrected` in the same transaction. Japan requires a correction that shows its link to the original, so voiding and issuing an unrelated new invoice is not enough ([japan-qualified-invoice.md](../domain/japan-qualified-invoice.md#other-documents-the-issuer-must-handle)); Germany accepts any practical correction method while the invoice type codes are unsettled ([germany-e-invoicing.md](../domain/germany-e-invoicing.md#errors-corrections-and-recurring-invoices)).

**Immutability is enforced by triggers**, not only in Go: an issued invoice may only change its status to `voided` or `corrected`; its lines, tax breakdown, and parties cannot change; insert-only tables reject `UPDATE` and `DELETE`.

## Consequences

**Positive**

- The rules an interviewer will ask about (tenant isolation, immutable invoices, gapless numbers, exact money) hold even if the Go code is wrong, and each one has a database test (acceptance criteria in the design document).
- One tax engine and one document table serve both countries; the differences are data (rates, currency, rounding mode).
- Webhook events are written in the same transaction as the change they report (transactional outbox through `pgmq`), so there are no lost or phantom events.

**Negative, and how we handle it**

- **Issues on one series are serialized** by the counter lock. Each holds it for a few milliseconds, far more than the free tier will carry. Tenants that need more throughput can use several series.
- **A separate login role** has to work through the Supabase pooler, and `pgmq` needs grants for it. Both are unverified and are checked first when the schema is built; if the pooler cannot use the role, an ADR revisits D2.
- **Triggers hide logic from the Go code.** Each trigger is short, covered by a test, and listed in the design document.
- **A decimal library is a new dependency.** Its license and rounding behaviour (half away from zero) are checked when it is chosen.
- **The encryption key for webhook secrets** is one more secret to manage. Phase 4 reviews it together with field-level encryption.
- **Corrections in phase 1 are full restatements only.** Correcting an invoice that already has payments or credit notes, and correcting a credit note, are rejected until carrying them over is designed.
- **Scope cuts (D7)** mean later migrations: widening `check` constraints and adding allowance and charge tables. The design document lists them so the phase 1 schema does not block them.

## Follow-ups

- Verify the `jade_api` role through the transaction pooler, the `pgmq` grants, and the project's default privileges for new schemas.
- Choose the decimal library and record its license.
- Write the migration and its acceptance tests (design document, "Acceptance criteria for the migration").
