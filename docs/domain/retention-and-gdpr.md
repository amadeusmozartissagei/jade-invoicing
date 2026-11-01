# Invoice retention versus the GDPR right to erasure

Sources checked on 2026-11-01. This is an engineering summary, not legal advice.

## The conflict

An invoice holds personal data: the name and address of a sole proprietor or of a contact person. Under the GDPR, a person can ask for their data to be erased. Tax law, however, requires invoices to be kept for years:

| Jurisdiction | What must be kept | Period | Starts |
|---|---|---|---|
| Germany | Invoices (§ 14b UStG) | 8 years | End of the calendar year of issue |
| Germany | Accounting vouchers / books (§ 147 AO) | 8 / 10 years | End of the calendar year of the event |
| Japan | Issuer's copies of qualified invoices | 7 years | Two months after the end of the taxable period of issue |

Details and sources: [germany-e-invoicing.md](germany-e-invoicing.md), [japan-qualified-invoice.md](japan-qualified-invoice.md).

## How the GDPR resolves it

- Art. 6(1)(c): processing is lawful when it is necessary to comply with a legal obligation of the controller.
- Art. 17(3)(b): the right to erasure does not apply to the extent that processing is necessary to comply with a legal obligation under Union or Member State law.
- Art. 18: in some situations the data subject can ask for processing to be restricted instead.

[GDPR]

So the platform may keep invoice data for the statutory period even after an erasure request, but only that data, only for that purpose, and only until the period ends.

## Design implications

- **Split data by legal basis.** Issued invoices, their XML, and the customer details printed on them are kept under the retention obligation. Everything else (customer contact records, notes, unused drafts, login data) can be erased on request.
- **Erasure request flow:** erase what has no retention basis now, mark retained invoices as restricted (no use beyond tax compliance, hidden from normal search and export), and record when each retained item may be deleted.
- **Retention clock per document**, computed from the jurisdiction's rule, not a single "delete after N years" setting.
- **Deletion at expiry is a scheduled job**, but German law keeps documents longer while a tax assessment is still open (§ 147 Abs. 3 AO). Expired items should be listed for the tenant to confirm, not deleted silently.
- **The platform is most likely a processor for its tenants**, and the tenant (the invoicing company) the controller for its customers' data. Confirm this against GDPR Art. 4 and 28 before writing it into the README. The platform provides the tools (export, erasure, restriction, retention); the tenant decides. This split belongs in the threat model and in the README.
- **Audit log** entries for erasure and restriction must not themselves contain the erased data.

## Open questions

- Pseudonymisation of retained invoices is not possible, because the invoice must stay complete and unchanged. Confirm that restriction plus access control is the right answer.
- Japan's Act on the Protection of Personal Information (APPI) has its own rules on deletion requests. Not researched yet.

## Sources

- **[GDPR]** Regulation (EU) 2016/679, Articles 6, 17, 18: https://eur-lex.europa.eu/eli/reg/2016/679/oj/eng
