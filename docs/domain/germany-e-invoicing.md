# Germany: invoices and mandatory B2B e-invoicing (E-Rechnung)

Sources checked on 2026-11-01. This is an engineering summary, not tax advice.

The Federal Ministry of Finance (BMF) has issued three letters on the E-Rechnung: the base letter of 15 Oct 2024, an amending letter of 15 Oct 2025, and a supplementary letter of 15 Oct 2026. They are cited by margin number (Rn.) and by the sections of the VAT Application Decree (UStAE) that they amend.

## Invoice types

§ 14 Abs. 1 UStG defines three terms: [UStG-14]

- **Rechnung:** any document that bills a supply or service.
- **Elektronische Rechnung (E-Rechnung):** an invoice issued, transmitted, and received in a **structured electronic format** that allows electronic processing. The format must follow the European standard for e-invoicing (EN 16931) and the syntax list under Directive 2014/55/EU. Alternatively, the parties may agree on another format if the required data can be extracted correctly and completely into a compliant format.
- **Sonstige Rechnung:** any other invoice, on paper or in another electronic format. **A plain PDF is a sonstige Rechnung.**

## Who must issue an E-Rechnung

- For supplies **between businesses established in Germany**, the invoice must be an E-Rechnung. Supplies that are tax-exempt under § 4 Nr. 8 to 29 UStG are excluded. [UStG-14]
- The obligation also covers self-billed invoices (Gutschrift) and reverse-charge invoices (§ 13b UStG) between domestic businesses. [BMF-2025 Rn. 17]
- Invoices must be issued within six months of the supply. [UStG-14]
- B2C invoices are not covered by the obligation.

**Exceptions.** Small-amount invoices up to €250 (Kleinbetragsrechnungen), invoices from small businesses (Kleinunternehmer), and travel tickets (Fahrausweise) may always be issued as a sonstige Rechnung. [BMF-2025 Rn. 22] They also do not need a sequential invoice number. [BMF-2025, UStAE 14.5 Abs. 14]

**Receiving.** Every domestic business must be able to receive E-Rechnungen, including small businesses. An ordinary e-mail inbox is enough; no separate inbox is needed. If a recipient cannot or will not accept an E-Rechnung, it has no right to demand another format, and the issuer has met its duty if it issued an E-Rechnung and can prove it tried to deliver it (e.g. a send log). [BMF-2025, UStAE 14.1 Abs. 5]

## Transition timeline

From § 27 Abs. 38 UStG: [UStG-27]

| Supply date | What the issuer may still send instead of an E-Rechnung |
|---|---|
| 1 Jan 2025 to 31 Dec 2026 | Paper, or another electronic format (e.g. PDF) with the recipient's consent |
| 1 Jan 2027 to 31 Dec 2027 | Same, but only if the issuer's total turnover in the previous year was **at most €800,000**. EDI (Recommendation 94/820/EC) with the recipient's consent is also still allowed |
| From 1 Jan 2028 | E-Rechnung for all domestic B2B supplies |

## Accepted formats

- **XRechnung:** the German CIUS (Core Invoice Usage Specification) of EN 16931, maintained by KoSIT on behalf of the IT-Planungsrat. Version 3.0 has been in force since 1 Feb 2024 and stays in force until at least 31 Jul 2027 (latest specification 3.0.2). XRechnung 4.0 was published as a preliminary version in September 2026, with the final release expected in early spring 2027. Syntaxes: UBL 2.1 and UN/CEFACT CII. [XRECHNUNG]
- **ZUGFeRD:** a hybrid format from FeRD. It embeds CII XML in a PDF/A-3 file. The current version is 2.5.2 (4 Aug 2026). [ZUGFERD]
- **ZUGFeRD from version 2.0.1 qualifies, except the MINIMUM and BASIC-WL profiles.** [BMF-2024 Rn. 25, 30], [BMF-2025, UStAE 14.1 Abs. 14]
- Other EN 16931 formats are allowed too, for example Factur-X (France) or Peppol BIS Billing. Which allowed format to use is a civil-law matter between the parties. [BMF-2024 Rn. 26, 27]
- A purely structured E-Rechnung needs no human-readable copy, because a viewer can render it. [BMF-2025, UStAE 14.4]
- **In a hybrid invoice, the structured part leads.** If the XML and the PDF disagree, the XML data prevail. [BMF-2024 Rn. 31], [BMF-2025, UStAE 14.4]
- If the PDF part shows different mandatory data (e.g. another description or tax amount), it may count as **a second invoice**, and § 14c UStG (tax shown on an invoice is owed) must be checked. Small technical differences such as a shortened description or rounding differences are tolerated. Input VAT can be deducted only from the structured part. [BMF-2024 Rn. 32]

## Content and transmission

- **All mandatory VAT items must be in the structured part.** The service description there must identify the service clearly. Extra detail may go in an attachment inside the E-Rechnung (e.g. a PDF timesheet). A link does not count. [BMF-2024 Rn. 35]
- Allowed delivery channels include e-mail, an API, a shared storage location within a group, and download from a web portal. The parties agree on the channel. [BMF-2024 Rn. 36]
- A business may use an external service provider to create or send E-Rechnungen, but it must make sure the provider meets the formal requirements. [BMF-2024 Rn. 37]
- **Sending the same E-Rechnung file more than once is harmless**, as long as it is the same invoice. [BMF-2024 Rn. 39]
- A future reporting system (Meldesystem) is planned and will define transmission channels again. [BMF-2024 Rn. 41]
- Official validator: https://github.com/itplr-kosit/validator, with the XRechnung configuration at https://github.com/itplr-kosit/validator-configuration-xrechnung. [XRECHNUNG]

## Errors, corrections, and recurring invoices

- **Format error:** the file does not match an allowed syntax or its technical rules. Whatever the error, the file is then **not an E-Rechnung** but a sonstige Rechnung. A validation tool can detect it. [BMF-2025 Rn. 6a]
- **Business-rule error:** the file breaks the format's business rules, for example a missing required field (BT-10 Buyer reference in XRechnung) or a tax amount that does not match the stated rate. Validators report these as critical errors. [BMF-2025 Rn. 6b]
- **Content error:** a breach of the mandatory VAT items in §§ 14(4) and 14a UStG makes the invoice not compliant. Business-rule errors that do not touch those items (e.g. a missing BT-10) are irrelevant for VAT. A content error can also exist when validation passes, e.g. a wrong tax rate. [BMF-2025 Rn. 35a]
- **Validation:** a business may rely on the result of a suitable validator for format and business rules, and keeping the validation report as evidence is recommended. Validation does not replace the recipient's own check. [BMF-2025 Rn. 35a]
- **A correction of an E-Rechnung must itself be an E-Rechnung**, using the matching invoice type. Sending the missing data in another form is not enough. A valid correction takes effect retroactively from the original issue date. [BMF-2024 Rn. 49]
- If an E-Rechnung was required but a sonstige Rechnung (e.g. a PDF) was sent, the buyer cannot deduct input VAT from it. It can be fixed by issuing an E-Rechnung that clearly refers to the original as corrected, with retroactive effect. [BMF-2024 Rn. 55 to 57]
- **No correction needed** when the taxable amount falls after issue under § 17 UStG, for example early-payment discounts (Skonti), price reductions for defects, a reversed supply, or year-end volume rebates. For rebates agreed in advance, the invoice only has to say that such a reduction exists, and a clear reference to the contract is enough. [BMF-2024 Rn. 51], [BMF-2025 Rn. 51a]
- **Final invoices after advance payments** (Endrechnung) cannot yet be represented fully in the structured part. The BMF suggests a remainder invoice (Restrechnung) instead. [BMF-2024 Rn. 47, 48] Until **30 Jun 2030** it tolerates either of two workarounds: deduct the advance payments as one sum in field BT-113 ("Paid amount") with the details in the free-text field BT-22 ("Invoice note"), or put the deduction in an embedded unstructured attachment that the structured part refers to explicitly. [BMF-2026 Rn. 3, 4]
- Advance or partial invoices issued before the issuer's E-Rechnung obligation started need not be E-Rechnungen, even if the supply and payment come later. [BMF-2026 Rn. 2]
- **Correction document types are not fixed yet.** The BMF says it is not technically settled which invoice type code represents a replacing correction, a cumulative correction, or a commercial credit note. For now, any practical correction method with a suitable invoice type is accepted, as long as the original and the corrections together show the final invoice data clearly. A self-billed credit note can correct an E-Rechnung if agreed beforehand. [BMF-2026 Rn. 20, 21, UStAE 14.11 Abs. 2]
- **A business-rule error does not stop a file from being an E-Rechnung for VAT purposes.** Technically, any broken EN 16931 business rule means the file does not conform to the standard, but that view does not decide VAT questions. For VAT, only a format error disqualifies the file, and a business-rule error matters only if it affects a mandatory item. [BMF-2026 Rn. 22, 23]
- Sending a sonstige Rechnung where an E-Rechnung was required is not an administrative offence under § 26a UStG. [BMF-2026 Rn. 28] The buyer's input VAT deduction still follows BMF-2024 Rn. 55 to 59.
- An issuer that could not know, with due care, that its customer was a business acting for its business is not faulted for not sending an E-Rechnung. For recurring mass business with mixed customers, asking once at contract start or change is enough. [BMF-2026 Rn. 5, 6]
- **Correction needed** when the scope or content of the supply changes. A self-billed credit note (Gutschrift) can do this if agreed beforehand, and it must refer clearly to the original invoice. [BMF-2025 Rn. 51b]
- **Recurring supplies** (Dauerschuldverhältnis, e.g. rent): one E-Rechnung for the first period is enough if the contract is attached or the invoice clearly says it is a recurring invoice. If the mandatory items change, the original must be corrected or a new E-Rechnung issued. [BMF-2025, UStAE 14.1 Abs. 19] The invoice number must be in the structured part; a unique number from the contract (e.g. a tenant number) is enough. [BMF-2025, UStAE 14.5 Abs. 12]

## Supplementary letter of 15 Oct 2026

The rules from this letter are placed in the sections where they belong (corrections, final invoices, business-rule errors). The rest:

- **Applies** to supplies after 31 Dec 2024, with the 2024 transitional rules (Rn. 62 to 65) until 31 Dec 2027. An E-Rechnung issued **until 31 Dec 2026** that meets the 2024 and 2025 letters but not this letter's extra requirements is not objected to, including for input VAT deduction. [BMF-2026, Anwendungsregelung]
- **Mapping mandatory items to EN 16931 fields.** KoSIT and FeRD publish a table that maps each § 14 and § 14a UStG item to a business term (BT) or business group (BG). The tax authority recommends using its current version. [BMF-2026 Rn. 8, 9] Summary: [germany-en16931-mapping.md](germany-en16931-mapping.md).
  - https://xeinkauf.de/aktuelles/xrechnung/pflichtangaben-nach-ustg-en16931
  - https://www.ferd-net.de/faqs/pflichtangaben-in-e-rechnungen
- **Service description.** The structured part must state the type, scope, and content of the service (and its place, if relevant) clearly enough to determine the tax treatment: supply versus service, place of supply, and the tax rate. Not every detail is needed. Extra detail such as timesheets can go in an embedded attachment. [BMF-2026 Rn. 10 to 12, UStAE 14.5 Abs. 15]
- **Construction services** may list only the trades with their totals in the structured part, with a detailed bill of quantities in an embedded human-readable attachment that the structured part refers to. [BMF-2026 Rn. 13, 14]
- **Rebate agreements** (bonuses, Skonti, discounts) whose amount is not known at invoice time must be referred to in the invoice. A general reference is accepted if the written agreement can be produced quickly. A later change to the agreement does not require a correction. [BMF-2026 Rn. 15, 16, UStAE 14.5 Abs. 19]
- **Public administration** customers: the VAT e-invoicing rules apply only when the authority acts as a business. Separate public-procurement e-invoicing rules (e.g. the federal ERechV, since 27 Nov 2020) apply in parallel, and the stricter rule wins. [BMF-2026 Rn. 24, 25]

## BMF FAQ (as of October 2026)

The BMF's FAQ page restates the three letters in plain language. It says itself that its answers are orientation only, with no legal or binding effect, so the letters stay the primary source. Points it adds, or states more clearly than the letters: [BMF-FAQ]

- **Domestic business.** A business counts as domestic if it has its seat, management, or a fixed establishment involved in the supply in Germany. A foreign business that is only VAT-registered in Germany may say so on its invoice to explain why it sends no E-Rechnung, and the recipient may rely on that statement. [BMF-FAQ Q3]
- **More cases where a sonstige Rechnung is allowed:** supplies to legal persons that are not businesses (for example many associations and state bodies) and certain supplies to consumers connected with real estate, in addition to small-amount invoices, travel tickets, and small businesses. [BMF-FAQ Q4, Q5]
- **Public administration (B2G).** The VAT rules and the public-procurement rules (ERechV) apply side by side with no ranking, so the stricter one wins. Example from the FAQ: the VAT obligation starts above €250, the federal ERechV from €1,000, and the ERechV requires its own submission platform. [BMF-FAQ Q4a]
- **BT-10 (Buyer reference) in B2B.** No Leitweg-ID is needed between businesses. Where a format makes BT-10 mandatory, a placeholder such as `-` is enough for VAT purposes. [BMF-FAQ Q6]
- **Validation** is not a legal precondition, but it is recommended at creation and before sending. The BMF names no specific validator. [BMF-FAQ Q7]
- **Collective invoice (Sammelrechnung).** The calendar month may be given as the supply period, so several supplies in one month can go on one E-Rechnung (§ 31 Abs. 4 UStDV). [BMF-FAQ Q7b]
- **Corrections of single lines** may be made in a later E-Rechnung, as long as that document refers specifically and unambiguously to the original. [BMF-FAQ Q7b]
- **Cash sales** follow the same rules: above €250 an E-Rechnung is due unless the issuer can and does use the transition rules. A till receipt can be issued first and corrected later by an E-Rechnung. [BMF-FAQ Q10]
- **The recipient has a civil-law claim to receive an E-Rechnung**, even though not issuing one is not a fine-able offence. [BMF-FAQ Q12c]
- **Viewer.** The tax authority runs a free viewer for XRechnung and ZUGFeRD files at https://www.e-rechnung.elster.de. [BMF-FAQ Q12a]
- **Work in progress at the standards bodies:** a gross-price invoice model and harmonised use of invoice type codes (BT-3). [BMF-FAQ Q15]
- The FAQ does not say that a PDF invoice became invalid in a new way. A plain PDF has been a sonstige Rechnung since 1 Jan 2025, allowed only under the transition rules. [BMF-FAQ Q2, Q11]

## Calculation and rounding

- **The tax amount must be calculated exactly to the cent and rounded commercially** (kaufmännisch: 0.5 cent and above rounds away from zero). [BMF-2026 Rn. 17, UStAE 14.5 Abs. 20]
- The EN 16931 core data model calculates from net amounts. For prices that are fixed gross (e.g. books under fixed book prices), this can cause rounding differences that break either the standard or the tax rounding rule. A gross-price format would solve it, but XRechnung and ZUGFeRD have none yet. [BMF-2026 Rn. 18, 19]
- The EN 16931 totals, VAT breakdown, and decimal rules are in [germany-en16931-calculation.md](germany-en16931-calculation.md).
- Until **31 Dec 2027**, a gross-price E-Rechnung may show only the net totals per tax rate, the rate, and the tax amount in the structured part, if an embedded human-readable attachment lists the line items that add up to those totals and the structured part refers to it. [BMF-2026 Rn. 19]

## Required items (Pflichtangaben)

§ 14 Abs. 4 UStG: [UStG-14]

1. Full name and address of the supplier and the recipient
2. The supplier's tax number (Steuernummer) or VAT ID (USt-IdNr.)
3. Issue date
4. A **sequential, unique invoice number** (one or more number series)
5. Quantity and description of goods, or scope and type of service
6. Date of supply (or date of payment received for advance payments, if known and different from the issue date)
7. Net amount broken down by tax rate and exemption, plus agreed reductions not already included
8. Tax rate and tax amount, or a note that an exemption applies
9. In the cases of § 14b Abs. 1 Satz 5, a note on the recipient's retention duty
10. The word "Gutschrift" if the recipient issues the invoice (self-billing)

In an E-Rechnung, the mandatory items must be in the structured part. [BMF-2025, UStAE 14.5]

**Small-amount invoice** (total ≤ €250, § 33 UStDV): supplier name and address, issue date, quantity and description, gross amount and the tax rate (or an exemption note). [UStDV-33]

## Document titles on the PDF

Checked on 2026-11-22. Covers the German title printed on the PDF for the three document types: `invoice` (UNTDID 1001 code 380), `credit_note` (381), and `corrected_invoice` (384).

- **No title is required by law, except "Gutschrift" for self-billing.** An invoice is any document that bills a supply, "gleichgültig, wie dieses Dokument im Geschäftsverkehr bezeichnet wird" (whatever it is called in business). The list of mandatory items in § 14 Abs. 4 Satz 1 contains no title; the only wording it prescribes is the word "Gutschrift" in Nr. 10. [UStG-14] (Special cases under § 14a UStG need fixed notes such as "Steuerschuldnerschaft des Leistungsempfängers" for reverse charge; those are notes, not titles. [BMF-2013 II.7])
- **"Gutschrift" in the statute means self-billing.** § 14 Abs. 2 **Satz 5** (not Satz 2; the sentences were renumbered, and older BMF texts still cite Satz 2) lets the recipient issue the invoice if agreed beforehand, and calls that a Gutschrift. § 14 Abs. 4 Satz 1 Nr. 10 requires the word "Gutschrift" when the recipient, or a third party it engaged, issues the invoice under Abs. 2 Satz 5. [UStG-14]
- **A supplier's credit note is not a Gutschrift in the VAT sense.** The UStAE says a cancellation or correction of the original invoice that everyday language also calls a Gutschrift is not one. [UStAE-14.3 Abs. 1 Satz 6, Abs. 2 Satz 5] The BMF calls this a "kaufmännische Gutschrift" (commercial credit note). [BMF-2013 I.2]
- **Calling it "Gutschrift" anyway is harmless for VAT, but not useful.** Using the word on a document that is not a VAT Gutschrift is "umsatzsteuerrechtlich unbeachtlich", and the label alone does not make § 14c UStG apply (tax shown on an invoice is owed). [BMF-2013 I.2] The same letter added this rule as a sentence to UStAE 14c.1 Abs. 3. [BMF-2013 II.8] § 14c UStG makes the issuer owe tax that it shows too high or without being entitled to show it. [UStG-14c] So the risk under § 14c comes from the tax amounts on the document, not from its title.
- **Self-billing may use other languages' terms.** Besides "Gutschrift", the BMF accepts the term used in another official EU language version of Art. 226 Nr. 10a of the VAT Directive, e.g. "Self-billing". Other terms do not meet § 14 Abs. 4 Satz 1 Nr. 10, but input VAT is not denied for that alone if the term is clear enough (e.g. "Eigenfaktura"). [BMF-2013 II.6, UStAE 14.5 Abs. 24]
- **A correction is a document that refers to the original.** An invoice can be corrected when it lacks items under § 14 Abs. 4 or § 14a UStG or has wrong items. Only the missing or wrong items need to be sent, in a document "das spezifisch und eindeutig auf die Rechnung bezogen ist" (specifically and unambiguously related to the invoice), with the same form and content requirements as an invoice under § 14 UStG. [UStDV-31 Abs. 5] The UStDV names no title for this document.
- **The BMF uses "Rechnungsberichtigung" for corrections** and lists replacing corrections, cumulative corrections, and commercial credit notes as common kinds; which invoice type code fits each is not settled yet. [BMF-2026 Rn. 20, 21]
- **Reference to the original: the rule is "specific and unambiguous", not a fixed list.** The sources read require a specific and unambiguous reference to the original invoice [UStDV-31 Abs. 5], [BMF-FAQ Q7b, Q12b], and for an E-Rechnung corrected by self-billing also a prior agreement [BMF-2026, UStAE 14.11 Abs. 2 Satz 7]. None of them says in so many words that the original's number and date must be printed.

**Titles chosen for the PDF** (a design choice within the rules above, not a statutory requirement; decided 2026-11-22). One title per type code, so the title follows from BT-3 alone. A full reversal (a void) could also be called "Stornorechnung" [UStAE-14.3 Abs. 2 Satz 5]; it was not chosen because both cases share code 381:

| Document type | UNTDID 1001 | German title | Basis |
|---|---|---|---|
| `invoice` | 380 | Rechnung | § 14 Abs. 1 UStG term [UStG-14] |
| `credit_note` (full or partial) | 381 | Rechnungskorrektur | "Korrektur" in [UStAE-14.3 Abs. 1 Satz 6, Abs. 2 Satz 5] |
| `corrected_invoice` | 384 | Rechnungsberichtigung | "berichtigt" in [UStDV-31 Abs. 5]; heading 3.1 in [BMF-2026] |

- **Never print "Gutschrift" on a supplier-issued document.** It is not harmful for VAT, but the statute reserves the word for self-billing, and the platform does not issue self-billed invoices.
- **Print the original's invoice number and issue date** on every 381 and 384 document. This is the simplest way to make the reference specific and unambiguous, and the EN 16931 model has a group for it (BG-3, preceding invoice reference). XRechnung expects a 384 to reference the preceding invoice in BG-3; see [germany-en16931-calculation.md](germany-en16931-calculation.md) (rule BR-DE-26).
- **Derive the PDF title from the XML type code (BT-3)**, so the two never disagree. The structured part leads [BMF-2024 Rn. 31]; a title is not a mandatory item, so the second-invoice risk in [BMF-2024 Rn. 32] concerns the amounts and items, but a mismatched title would still confuse the reader.

**Open points:**

- The current UStAE could not be read directly: the BMF's official VAT handbook site (usth.bundesfinanzministerium.de) answered with a bot check. UStAE 14.3 was read from an archived copy of the **2023 edition**. The wording of 14.3 Abs. 1 Satz 6 and Abs. 2 Satz 5 should be checked against the current UStAE, including any change by the 2024, 2025, or 2026 letters.
- UStAE 14c.1 Abs. 3 was read only as the amending text in [BMF-2013 II.8]. The 2023 edition of UStAE 14.3 cites it as Satz 3, so the sentence has moved since 2013.
- UStAE 14.11 (Rechnungsberichtigung) Abs. 1 was not read. Whether it names the original's number and date as required content is unknown.
- Whether a full replacement invoice (384) leaves both the original and the replacement as invoices with tax shown, and so a § 14c risk, unless the original is cancelled first: no primary source read answers this.
- "Stornorechnung", "Rechnungskorrektur", and "Rechnungsberichtigung" are not defined terms in UStG or UStDV; they come from administrative wording. No source makes one of them mandatory.

## VAT rates

19% standard, 7% reduced. [UStG-12]

## Retention

- Invoices: **8 years**, from the end of the calendar year in which the invoice was issued. [UStG-14b], [BMF-2025 Rn. 60]
- Under the Fiscal Code, accounting vouchers (Buchungsbelege) are kept 8 years, books and annual statements 10 years, and other tax-relevant documents 6 years. A period does not end while the documents still matter for a tax assessment whose limitation period has not expired. [AO-147]
- An invoice is kept in the format it was received in. For an E-Rechnung, **at least the structured part** must be kept intact in its original form and stay machine-readable for the tax authority. [BMF-2025 Rn. 60, UStAE 14b.1 Abs. 1 and 5]
- Storing E-Rechnungen outside a GoBD-compliant system is, for VAT purposes, not by itself a breach of § 14b UStG. Other rules (AO, GoBD) still apply. [BMF-2025 Rn. 60]

## Design implications

- **The structured invoice is the source of truth.** Generate XRechnung XML or ZUGFeRD XML from the invoice model, and render the PDF from the same model, so the two can never disagree.
- **Validate every generated E-Rechnung** with the KoSIT validator, and **store the validation report** next to the invoice as evidence. The validator is Java, so it runs in CI and possibly in a separate validation step, not in the Vercel functions.
- **Invoice numbers:** sequential and unique per tenant and series. This needs a concurrency-safe counter in Postgres.
- **Data model** must hold every Pflichtangabe in structured fields: full addresses for both parties, tax number or VAT ID, supply date separate from issue date, and net and tax per rate.
- **Corrections** are new E-Rechnungen with the matching invoice type that reference the original. Issued invoices are never edited. Payment discounts (Skonto) and later rebates are recorded as events, not as invoice corrections.
- **Advance payments:** support remainder invoices (Restrechnung) first. If final invoices are added, use the BT-113 / BT-22 method, which is tolerated until 30 Jun 2030.
- **Tax engine for Germany:** compute tax to the cent and round commercially (half away from zero). Start with net-price invoices only; gross-price invoices need the attachment workaround and can wait.
- **Field mapping:** take the KoSIT/FeRD mapping table as the reference for which model field goes into which BT, and keep a test per mandatory item.
- **Validation severity:** treat format errors as blocking, and business-rule errors as blocking too, because the platform should only send files that conform technically, even though for VAT a business-rule error outside the mandatory items is harmless.
- **Customer type:** store whether each customer is a business, and whether it is established in Germany, set at onboarding, because that decides whether an E-Rechnung is required. A tenant that is only VAT-registered in Germany needs an optional invoice note saying so. [BMF-FAQ Q3]
- **BT-10 default:** when the buyer gives no reference, fill BT-10 with `-` so XRechnung validation passes. [BMF-FAQ Q6]
- **Monthly collective invoices** need a supply period (a calendar month), not only a supply date, in the data model. [BMF-FAQ Q7b]
- **Webhook and e-mail redelivery is safe** for the invoice file itself, because resending the identical file is allowed. Retries must never produce a changed file under the same invoice number.
- **The platform acts as an external service provider** for its tenants, so the tenant stays responsible for compliance and the platform must make that possible (validation, complete fields, archive).
- **Recurring invoices** are a document type of their own, linked to a contract reference.
- **Archive the XML exactly as sent.** Store the bytes and a hash, and never regenerate them.
- **Delivery log.** Keep proof of each delivery attempt, because it shows the issuer met its duty when a recipient refuses.
- **Tenant setting:** whether the tenant is under €800,000 turnover (allowed to send PDF until end of 2027). After 2027 this setting no longer matters.

## Open questions

- The mapping of mandatory items to EN 16931 fields is in [germany-en16931-mapping.md](germany-en16931-mapping.md). Check for a newer version of the KoSIT/FeRD table after the BMF letter of 15 Oct 2026.
- The ERechV threshold of €1,000 for federal B2G invoices is taken from the BMF FAQ, not from the ERechV itself. Check the regulation before the platform supports B2G.
- Sections 5 (retention) and the transitional rules (Rn. 62 to 65) of the 2024 letter were not read closely, because the 2025 letter rewrote Rn. 60 and § 27 Abs. 38 UStG is already covered above.
- Which XRechnung syntax to generate first (UBL or CII). CII is shared with ZUGFeRD, so it may be the cheaper first choice.
- Whether to support XRechnung 4.0 from the start or only 3.0.x.

## Sources

- **[UStG-14]** § 14 UStG: https://www.gesetze-im-internet.de/ustg_1980/__14.html
- **[UStG-14b]** § 14b UStG: https://www.gesetze-im-internet.de/ustg_1980/__14b.html
- **[UStG-27]** § 27 Abs. 38 UStG: https://www.gesetze-im-internet.de/ustg_1980/__27.html
- **[UStG-12]** § 12 UStG: https://www.gesetze-im-internet.de/ustg_1980/__12.html
- **[UStDV-33]** § 33 UStDV: https://www.gesetze-im-internet.de/ustdv_1980/__33.html
- **[UStG-14c]** § 14c UStG: https://www.gesetze-im-internet.de/ustg_1980/__14c.html (checked 2026-11-22)
- **[UStDV-31]** § 31 UStDV: https://www.gesetze-im-internet.de/ustdv_1980/__31.html (checked 2026-11-22)
- **[UStAE-14.3]** UStAE Abschnitt 14.3 *Rechnung in Form der Gutschrift*, BMF, Amtliche Umsatzsteuer-Handausgabe **2023** (checked 2026-11-22).
  - Original: https://usth.bundesfinanzministerium.de/usth/2023/A-Umsatzsteuergesetz/IV-Steuer-und-Vorsteuer/Paragraf-14/ae-14-3.html (answered with a bot check on 2026-11-22)
  - Copy read (Wayback Machine, 2026-06-15): https://web.archive.org/web/20260615043358/https://usth.bundesfinanzministerium.de/usth/2023/A-Umsatzsteuergesetz/IV-Steuer-und-Vorsteuer/Paragraf-14/ae-14-3.html
  - SHA-256 of the copy (raw `id_` capture): `6a1f957574099c5b0771f00f3fb5c97eaae52a80e58285202d361bf0ca630d80`
- **[BMF-2013]** BMF-Schreiben vom 25. Oktober 2013, *Ausstellung von Rechnungen - Änderungen der §§ 14, 14a UStG durch das Amtshilferichtlinie-Umsetzungsgesetz*, GZ IV D 2 - S 7280/12/10002, DOK 2013/0956687. Cited by section (I.2 = Rechnungsangabe "Gutschrift"; II.6 to II.8 = amendments to UStAE 14.5 Abs. 24, 14a.1, 14c.1 Abs. 3). It cites the statute's old numbering (§ 14 Abs. 2 Satz 2 for Gutschrift). (checked 2026-11-22)
  - Copy read: hosted by IHK München, https://www.ihk-muenchen.de/ihk/documents/Recht-Steuern/Steuerrecht/Neuer-Ordner/bmf-schreiben-vom-25.-oktober-2013.pdf (14 pages)
  - SHA-256 of the copy: `094725620ed79bfb04ab551c99db07e496b6459be6584dc011ba4af9bb091e51`
- **[AO-147]** § 147 AO: https://www.gesetze-im-internet.de/ao_1977/__147.html
- **[BMF-2024]** BMF-Schreiben vom 15. Oktober 2024, *Ausstellung von Rechnungen nach § 14 UStG; Einführung der obligatorischen elektronischen Rechnung bei Umsätzen zwischen inländischen Unternehmern ab dem 1. Januar 2025*, GZ III C 2 - S 7287-a/23/10001 :007, DOK 2024/0883282, BStBl I S. 1320.
  - Copy read: hosted by the Bundesarchitektenkammer, https://bak.de/wp-content/uploads/2024/12/2024-10-15-BMF-Einfuehrung-e-rechnung.pdf (18 pages; PDF created 2024-10-15, modified 2024-12-12, so the host may have re-saved it)
  - SHA-256 of the copy: `6ac0189e94078a2822cf9835f977f07feca8b1efaf4ee717c9785ad8fd737ac0`
- **[BMF-2026]** BMF-Schreiben vom 15. Oktober 2026, *Einführung der obligatorischen elektronischen Rechnung bei Umsätzen zwischen inländischen Unternehmern ab dem 1. Januar 2025; Ergänzende Regelungen*, GZ III C 5 - S 7287-a/00019/021/017, DOK COO.7005.100.3.15923106.
  - Original: https://www.bundesfinanzministerium.de/Content/DE/Downloads/BMF_Schreiben/Steuerarten/Umsatzsteuer/Umsatzsteuer-Anwendungserlass/2026-10-15-obligatorische-elektronische-rechnung.pdf (downloaded by hand by the owner, 10 pages)
  - Copy in this repository: [sources/bmf-2026-10-15-e-rechnung-ergaenzende-regelungen.pdf](sources/bmf-2026-10-15-e-rechnung-ergaenzende-regelungen.pdf) (an administrative decree, which § 5 Abs. 1 UrhG excludes from copyright: https://www.gesetze-im-internet.de/urhg/__5.html)
  - SHA-256 of the copy read: `af65d13f4d06bec52c425a0a021b2526f5acaa70a8c8b8fc628d9785547b12e6`
- **[BMF-2025]** BMF-Schreiben vom 15. Oktober 2025, *Einführung der obligatorischen elektronischen Rechnung bei Umsätzen zwischen inländischen Unternehmern ab dem 1. Januar 2025; Anpassung des Umsatzsteuer-Anwendungserlasses*, GZ III C 2 - S 7287-a/00019/007/243.
  - Original: https://www.bundesfinanzministerium.de/Content/DE/Downloads/BMF_Schreiben/Steuerarten/Umsatzsteuer/Umsatzsteuer-Anwendungserlass/2025-10-15-einfuehrung-obligatorische-e-rechnung.pdf
  - Copy read (Wayback Machine, 2026-09-16): https://web.archive.org/web/20260916164508/https://www.bundesfinanzministerium.de/Content/DE/Downloads/BMF_Schreiben/Steuerarten/Umsatzsteuer/Umsatzsteuer-Anwendungserlass/2025-10-15-einfuehrung-obligatorische-e-rechnung.pdf?__blob=publicationFile&v=5
  - SHA-256 of the copy: `b47bd1b6f3f8386774a3d37976a385b3443537bc2a2891b64d2eb58cfee16d7f`
- **[BMF-FAQ]** BMF, *Fragen und Antworten zur Einführung der obligatorischen (verpflichtenden) E-Rechnung zum 1. Januar 2025*, "Stand: Oktober 2026", page dated 29 Oct 2026. Cited by question number.
  - Original: https://www.bundesfinanzministerium.de/Content/DE/FAQ/e-rechnung.html (saved as PDF from the browser by the owner on 2026-11-01, 12 pages)
  - Local copy, not committed (the page carries a BMF copyright notice and is not clearly an official decree under § 5 Abs. 1 UrhG): `sources/bmf-faq-e-rechnung-2026-11-01.pdf`, SHA-256 `7e2bd7d58b28c79371821c0b64ab209c18467fcca70e5bf60a0b1ff1baa9aff0`
- **[XRECHNUNG]** KoSIT, XRechnung: https://xeinkauf.de/xrechnung/
- **[ZUGFERD]** FeRD, ZUGFeRD: https://www.ferd-net.de/standards/zugferd
