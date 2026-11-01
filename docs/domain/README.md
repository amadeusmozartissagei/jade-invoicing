# Domain notes

Rules the platform must follow, each with a primary source. These are engineering summaries, not tax or legal advice. Check the sources again before relying on a detail, because rules and versions change.

| Note | Covers | Last checked |
|---|---|---|
| [japan-qualified-invoice.md](japan-qualified-invoice.md) | Japan's Qualified Invoice System: registration numbers, required items, tax rates, rounding, return and corrected invoices, retention | 2026-11-01 |
| [germany-e-invoicing.md](germany-e-invoicing.md) | German B2B e-invoicing: E-Rechnung definition, transition timeline, XRechnung and ZUGFeRD, required items, retention | 2026-11-01 |
| [germany-en16931-mapping.md](germany-en16931-mapping.md) | German mandatory invoice items mapped to EN 16931 business terms (KoSIT/FeRD table) | 2026-11-01 |
| [retention-and-gdpr.md](retention-and-gdpr.md) | Invoice retention versus the GDPR right to erasure | 2026-11-01 |

## Rules that shape the design most

1. **Issued invoices are immutable.** Both countries correct errors with a new document (corrected invoice, return invoice, credit note) that references the original.
2. **Japan rounds tax once per tax rate per invoice**, never per line.
3. **In Germany the structured XML is the invoice.** The PDF is a rendering of the same data, and the XML must be archived unchanged.
4. **German invoice numbers are sequential and unique**, so numbering needs a concurrency-safe counter.
5. **Retention is 7 years (Japan) or 8 years (Germany)** with different start dates, and it overrides the GDPR right to erasure for the invoice itself.

## Gaps to close

- The BMF FAQ (the BMF website blocks automated tools, so a person has to download it), and whether KoSIT/FeRD publish a newer mapping table after the BMF letter of 15 Oct 2026.
- EN 16931 calculation rules in detail. German tax rounding is settled: to the cent, commercial rounding.
- Japanese corporate number check digit, the NTA registration lookup API, and APPI deletion rules.
