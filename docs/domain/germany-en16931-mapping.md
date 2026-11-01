# Germany: mandatory invoice items mapped to EN 16931 fields

Source checked on 2026-11-01. This is an engineering summary of the KoSIT/FeRD recommendation, not tax advice. The BMF recommends using this table, in its current version, to decide which field holds which mandatory item. [BMF-2026 Rn. 8, 9]

The table is dated 25 Jul 2025, before the BMF letter of 15 Oct 2026. Where that letter adds rules (final invoices, gross prices, service description), they are noted below. Cardinalities and the original wording are in the source PDF.

## Core items (§ 14 Abs. 4 UStG)

| § 14 Abs. 4 | Item | EN 16931 fields |
|---|---|---|
| Nr. 1 | Supplier full name | BG-4 Seller: BT-27 Seller name, BT-28 Seller trading name |
| Nr. 1 | Supplier full address | BG-5 Seller postal address: BT-35, BT-36, BT-162 (address lines 1 to 3), BT-37 city, BT-38 post code, BT-39 country subdivision, BT-40 country code |
| Nr. 1 | Recipient full name | BG-7 Buyer: BT-44 Buyer name, BT-45 Buyer trading name |
| Nr. 1 | Recipient full address | BG-8 Buyer postal address: BT-50, BT-51, BT-163 (address lines 1 to 3), BT-52 city, BT-53 post code, BT-54 country subdivision, BT-55 country code |
| Nr. 2 | Supplier tax number or VAT ID | BG-4 Seller: BT-31 Seller VAT identifier **or** BT-32 Seller tax registration identifier |
| Nr. 3 | Issue date | BT-2 Invoice issue date |
| Nr. 4 | Sequential invoice number | BT-1 Invoice number |
| Nr. 5 | Quantity and description | BG-25 Invoice line: BT-129 Invoiced quantity; BG-31 Item information |
| Nr. 6 | Date of supply | BG-13 Delivery information: BT-72 Actual delivery date; or BG-14 Invoicing period / BG-26 Invoice line period. A period is given as start and end date; a month as its first and last day |
| Nr. 6 | Date of payment received, if known and different from the issue date | BT-7 Value added tax point date |
| Nr. 6a | Note "Versteuerung nach vereinnahmten Entgelten" (cash accounting) | BG-1 Invoice note; BT-7 or BT-8 Value added tax point date code |
| Nr. 7 | Net amount per tax rate and exemption | BT-131 Invoice line net amount; line allowances and charges (BT-136, BT-141); BT-146 Item net price; document allowances and charges (BT-92, BT-99); document totals (BT-106, BT-107, BT-109, BT-112); BG-23 VAT breakdown: BT-116 VAT category taxable amount |
| Nr. 7 | Reductions agreed in advance (rebates, bonuses, Skonto) | BT-20 Payment terms or BG-1 Invoice note, e.g. "Es bestehen Rabatt- oder Bonusvereinbarungen."; also BT-139 / BT-97 allowance reasons when the reduction is given as an allowance. XRechnung can carry Skonto terms in structured form in BT-20 |
| Nr. 8 | Tax rate | BT-152 Invoiced item VAT rate; BT-96 Document level allowance VAT rate; BG-23: BT-118 VAT category code, BT-119 VAT category rate |
| Nr. 8 | Tax amount | BG-23 VAT breakdown; BT-110 Invoice total VAT amount |
| Nr. 8 | Exemption note | BG-23: BT-120 VAT exemption reason text and BT-121 VAT exemption reason code; or BG-1 Invoice note |
| Nr. 9 | Note on the recipient's retention duty (§ 14b Abs. 1 Satz 5 cases) | BG-1 Invoice note |
| Nr. 10 | "Gutschrift" for self-billing | BT-3 Invoice type code **389** (self-billed invoice), plus the text "Gutschrift" in BG-1 Invoice note |

## Advance, partial, and final invoices (§ 14 Abs. 5 UStG)

| Item | EN 16931 fields |
|---|---|
| Final invoice: deduct advance payments and their tax | BG-1 Invoice note; BG-24 Additional supporting documents; BG-22: BT-113 Paid amount, BT-115 Amount due for payment |
| Invoice type for advance invoices | BT-3 code **386** (prepayment invoice) |
| Invoice type for partial invoices | BT-3 code **326** (partial invoice) |

The table says detailed final invoices cannot yet be shown in the core model and must use attachments. The BMF letter of 15 Oct 2026 adds a tolerated alternative until 30 Jun 2030: the advance payments as a sum in BT-113 with the details in BT-22. [BMF-2026 Rn. 3, 4]

## Special cases (§ 14a and § 22c UStG)

Out of scope for the MVP (domestic B2B), listed so the data model does not block them later.

| Case | EN 16931 fields |
|---|---|
| Reverse charge, EU (§ 14a Abs. 1) and domestic § 13b (§ 14a Abs. 5) | BT-32; BT-120 with the text "Steuerschuldnerschaft des Leistungsempfängers", BT-121 |
| Both parties' VAT IDs (§ 14a Abs. 1 and 7) | BT-31 Seller VAT identifier; BT-48 Buyer VAT identifier |
| Intra-community supply (§ 14a Abs. 3) | BT-120 with "Steuerfreie innergemeinschaftliche Lieferung" or the legal reference, BT-121; BT-31; BT-48 |
| New vehicles (§ 14a Abs. 4) | BG-32 Item attributes, one per attribute |
| Travel agents, margin scheme (§ 14a Abs. 6) | BG-1 Invoice note with the required wording |
| Triangular transactions (§ 14a Abs. 7) | BG-1 Invoice note; BT-31; BT-48 |
| Fiscal representative (§ 22c) | BG-11 Seller tax representative party: BT-62 name, BT-63 VAT identifier; BG-12 postal address |

## Design implications

- **The invoice model mirrors these business terms.** Name model fields after the BTs they map to (or keep a mapping table in code), so a reviewer can check each mandatory item against this page.
- **One test per row of the core table.** Each test builds an invoice and asserts that the generated XML has the item in the listed field.
- **Invoice type codes:** this table gives 389 (self-billing), 386 (advance), and 326 (partial). The code for a normal commercial invoice is 380, from the XRechnung rule BR-DE-17 (see [germany-en16931-calculation.md](germany-en16931-calculation.md#invoice-type-codes-bt-3)). Correction codes are still open (see the corrections section of [germany-e-invoicing.md](germany-e-invoicing.md)).
- **Supplier tax ID:** require at least one of BT-31 and BT-32 per tenant.
- **Supply date:** support a single date (BT-72) and a period (BG-14), since services often span a month.

## Sources

- **[KOSIT-FERD-2025]** KoSIT / FeRD, *Empfehlungen zu Pflichtangaben in einer E-Rechnung, Teil I: Pflichtangaben gemäß Umsatzsteuergesetz*, Stand 25 Jul 2025, 10 pages.
  - Download: https://xeinkauf.de/app/uploads/2025/07/Empfehlungen_Pflichtangaben_E-Rechnung_gem_UStG_20250725.pdf (linked from https://xeinkauf.de/xrechnung/ustg)
  - SHA-256 of the copy read: `51e71c78b4b03148923fffa0b7054d558e938376d0fd69f7c33f2ef49a1a9abc`
- **[BMF-2026]** See [germany-e-invoicing.md](germany-e-invoicing.md#sources).
