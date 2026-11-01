# Germany: EN 16931 calculation rules

Sources checked on 2026-11-01. This is an engineering summary, not tax advice.

The standard EN 16931-1 itself is sold by the standards bodies and was not read. This note works from the business rules as CEN publishes them in its validation artefacts, which the KoSIT validator runs for XRechnung, and from the XRechnung rules that KoSIT adds on top. Rule IDs are quoted so each claim can be checked against the rule text.

## Totals (BG-22)

| Total | Rule |
|---|---|
| BT-106 Sum of invoice line net amounts | = Σ BT-131 Invoice line net amount [BR-CO-10] |
| BT-107 Sum of document level allowances | = Σ BT-92 [BR-CO-11] |
| BT-108 Sum of document level charges | = Σ BT-99 [BR-CO-12] |
| BT-109 Invoice total without VAT | = Σ BT-131 − BT-107 + BT-108 [BR-CO-13] |
| BT-110 Invoice total VAT amount | = Σ BT-117 VAT category tax amount [BR-CO-14] |
| BT-112 Invoice total with VAT | = BT-109 + BT-110 [BR-CO-15] |
| BT-115 Amount due for payment | = BT-112 − BT-113 Paid amount + BT-114 Rounding amount [BR-CO-16] |

BT-106, BT-109, BT-112, and BT-115 are mandatory [BR-12 to BR-15]. [CEN-SCH]

## VAT breakdown (BG-23)

- An invoice has at least one VAT breakdown [BR-CO-18], and every line has a VAT category code (BT-151) [BR-CO-04].
- For standard-rated VAT (category `S`), there is **one breakdown per distinct rate**. Its taxable amount (BT-116) is the sum of the line net amounts at that rate, plus document charges and minus document allowances at that rate. [BR-S-08]
- **The tax is calculated once per breakdown**, from the taxable amount: BT-117 = BT-116 × (BT-119 / 100), rounded to two decimals. [BR-CO-17], [BR-S-09]
- Zero-rated (`Z`) and exempt (`E`) breakdowns have the same taxable-amount rule and a tax amount of 0. [BR-Z-08, BR-Z-09, BR-E-08, BR-E-09]
- XRechnung requires the rate (BT-119) in every breakdown, even for exempt categories. [BR-DE-14]

## Decimals

At most **two decimals** for every document amount: line net amount (BT-131), line and document allowances and charges and their base amounts, all totals (BT-106 to BT-115), and the breakdown amounts (BT-116, BT-117). [BR-DEC-01, 02, 05, 06, 09 to 20, 23 to 25, 27, 28] There is no such limit on the item net price (BT-146) or the quantity (BT-129).

## Line net amount (BT-131)

CEN's rules do not check how BT-131 is calculated, only that the lines add up to BT-106. The calculation is stated in Peppol BIS Billing 3.0, another EN 16931 profile: BT-131 = BT-129 Invoiced quantity × (BT-146 Item net price / BT-149 Item price base quantity) + Σ line charges − Σ line allowances, checked with a tolerance of 0.02. [PEPPOL-R120] The XRechnung rules add no calculation rule of their own. [XR-SCH]

## Validator tolerance versus the tax rule

**The validator accepts a VAT amount that is off by up to one whole currency unit.** In the CII binding, BR-CO-17 and BR-S-09 pass when the stated tax amount is within ±1 of the rounded product (for EUR, ±€1.00, not ±€0.01). [CEN-SCH-CII]

German tax law is stricter: the tax must be calculated exactly to the cent and rounded commercially. [BMF-2026 Rn. 17] So a file can pass validation with a tax amount that is wrong for VAT purposes. The platform's own tests must check the exact amount and must not rely on the validator for it.

## Invoice type codes (BT-3)

XRechnung warns unless BT-3 is one of: 326 partial invoice, 380 commercial invoice, 381 credit note, 384 corrected invoice, 389 self-billed invoice, and 875, 876, 877 for construction invoices. [BR-DE-17] A 384 corrected invoice should reference the preceding invoice (BG-3). [BR-DE-26]

This is a technical list. Which code represents which kind of correction is still not settled for VAT purposes (see the corrections section of [germany-e-invoicing.md](germany-e-invoicing.md)).

## Other XRechnung rules that shape the data model

All are fatal unless marked. [XR-SCH]

- Payment instructions (BG-16) are required [BR-DE-1]. For a credit transfer (payment means 30 or 58), the payee account (BG-17) is required [BR-DE-23-a]; with 58 (SEPA) it should be a valid IBAN (warning) [BR-DE-19].
- Seller contact (BG-6) with contact point, telephone, and e-mail is required [BR-DE-2, BR-DE-5 to BR-DE-7].
- Seller city and post code [BR-DE-3, BR-DE-4]; buyer city and post code [BR-DE-8, BR-DE-9].
- Buyer reference (BT-10) is required [BR-DE-15]. In B2B a `-` placeholder is enough for VAT purposes. [BMF-FAQ Q6]
- Seller VAT identifier (BT-31) or tax registration identifier (BT-32) is required when standard, zero-rated, or exempt categories are used. [BR-DE-16]
- The supply date should be given as BT-72, BG-14, or a BG-26 on every line (information only). [BR-DE-TMP-32]

## How this compares with Japan

Both countries calculate tax on the **per-rate total**, not per line: EN 16931 once per VAT breakdown, Japan once per tax rate per invoice. [BR-CO-17], [NTA-QA-57] One tax engine can serve both, with three differences set per jurisdiction:

| | Germany (EN 16931) | Japan |
|---|---|---|
| Currency unit | EUR, 2 decimals | JPY, no minor unit |
| Tax rounding | Commercial, to the cent [BMF-2026 Rn. 17] | Issuer's choice of up, down, or half up, to the yen [NTA-QA-57] |
| Rates | 19% and 7% | 10% and 8% |

How to round line net amounts is not regulated in either country as far as the sources above show. Rounding each line to the currency unit, half away from zero, is the platform's own choice; it satisfies the Peppol formula and keeps every stored amount within the decimal limits.

## Design implications

- **Tax engine:** round each line net amount to the currency unit; group lines by VAT category and rate; sum; compute tax once per group with the jurisdiction's rounding; derive the totals with the BR-CO formulas above. Store the breakdown and totals at issue time.
- **Exact arithmetic:** quantity and price may have more than two decimals, so the engine needs a decimal type; never a float.
- **Tests** assert the exact tax per group (not the validator's ±1 tolerance), and assert that the totals satisfy BR-CO-10 to BR-CO-16 exactly.
- **Tenant profile for Germany** needs a contact person, telephone, e-mail, and a bank account (IBAN) for payment instructions, or XRechnung validation fails.
- **Invoice types:** map an ordinary invoice to 380, a credit note to 381, and a corrected invoice to 384 with a reference to the original.

## Sources

- **[CEN-SCH]** CEN/TC 434, EN 16931 validation artefacts, release `validation-1.3.17` (2026-10-12), file `cii/schematron/abstract/EN16931-CII-model.sch` (the business rules with their message texts). Licence EUPL-1.2. https://github.com/ConnectingEurope/eInvoicing-EN16931/tree/validation-1.3.17
  - SHA-256 of the file read: `0b3e3e54380ee473b674c22051d63b8e06e7e8113470ea2d1de13052a97ff41c`
- **[CEN-SCH-CII]** Same release, file `cii/schematron/CII/EN16931-CII-model.sch` (the test expressions for CII), parameters `BR-CO-17` and `BR-S-09`.
  - SHA-256 of the file read: `864f96dff4819b39e0bdbda76c5396c78aee26d0f12676bb0b689df3230965ed`
- **[XR-SCH]** KoSIT, XRechnung Schematron `v2.6.0` (2026-08-31, "compatible with XRechnung 3.0"), file `src/validation/schematron/cii/XRechnung-CII-validation.sch`. Licence Apache-2.0. https://github.com/itplr-kosit/xrechnung-schematron/tree/v2.6.0
  - SHA-256 of the file read: `efc58eaa9d215d8d7c94557e1ed37de068ddb1f97fcfd1c5da800c06223a97c3`
- **[PEPPOL-R120]** OpenPeppol, Peppol BIS Billing 3.0 (May 2026 release), rule PEPPOL-EN16931-R120: https://docs.peppol.eu/poacc/billing/3.0/rules/ubl-peppol/PEPPOL-EN16931-R120/
- **[BMF-2026]**, **[BMF-FAQ]**: see [germany-e-invoicing.md](germany-e-invoicing.md#sources).
- **[NTA-QA-57]**: see [japan-qualified-invoice.md](japan-qualified-invoice.md#sources).
