# Japan: Qualified Invoice System (適格請求書等保存方式)

Sources checked on 2026-11-01. This is an engineering summary, not tax advice.

## What it is

Since 1 October 2023, a buyer can deduct consumption tax paid on purchases (仕入税額控除) only if it keeps a ledger and a **qualified invoice** (適格請求書, "invoice") from the seller. Only businesses registered with the National Tax Agency (NTA) as qualified invoice issuers (適格請求書発行事業者) may issue one. [NTA-EN]

## Registration number

- Format: `T` followed by 13 digits. [NTA-LEAFLET PDF p.21]
  - A corporation uses `T` + its corporate number (法人番号).
  - Other businesses (sole proprietors, associations without legal personality) get `T` + a 13-digit number.
- Registered issuers are published on the NTA announcement site (適格請求書発行事業者公表サイト), https://www.invoice-kohyo.nta.go.jp/. [NTA-EN PDF p.2]

## Required items

**Qualified invoice** (適格請求書): [NTA-EN]

1. Issuer name and registration number
2. Transaction date
3. Transaction details, marking items taxed at the reduced rate
4. Total amount per tax rate (tax-exclusive or tax-inclusive) and the applicable rate
5. Consumption tax amount per tax rate, in yen
6. Name of the recipient

**Simplified qualified invoice** (適格簡易請求書), allowed for retail, restaurants, taxis, and similar businesses that sell to many unspecified people: items 1 to 4, and item 5 may be the tax amount, the tax rate, or both. No recipient name. [NTA-EN]

There is no prescribed layout. An electronic record (電子インボイス) may replace paper, with the same required items. [NTA-LEAFLET PDF p.6]

## Tax rates

| Rate | Consumption tax portion | Local consumption tax portion | Tax-inclusive factor |
|---|---|---|---|
| 10% standard | 7.8% | 2.2% | 10/110 |
| 8% reduced (軽減税率) | 6.24% | 1.76% | 8/108 |

[NTA-EN p.3]

## Rounding rule (端数処理)

- When the per-rate tax amount has a fraction of a yen, round **once per tax rate per invoice**. [NTA-QA-57], [NTA-LEAFLET PDF p.8]
- The method is the issuer's choice: round up, round down, or round half up. [NTA-QA-57]
- Computing tax per line item, rounding each line, and summing the results is **not allowed**. [NTA-QA-57]

## Other documents the issuer must handle

[NTA-LEAFLET PDF p.9]

| Obligation | Rule |
|---|---|
| Issue on request | Must issue a qualified invoice when a taxable business customer asks |
| Return invoice (適格返還請求書) | Required for returns, discounts, and rebates. Exempt when the tax-inclusive amount is under ¥10,000 [NTA-QA-28] |
| Corrected invoice (修正した適格請求書) | If an issued invoice, simplified invoice, or return invoice has an error, issue a corrected one |
| Keep copies | Keep a copy of every issued document |
| False invoices | Prohibited, with penalties |

A return invoice carries the issuer name and registration number, the return date, the date of the original transaction, details of the return (with the reduced-rate mark), the return amount per tax rate, and either the tax amount or the rate per tax rate.

**How to correct an issued document.** When an issued qualified invoice, simplified invoice, or return invoice has an error, the issuer must give the buyer a corrected document (消法57の4④⑤). The NTA names two ways: issue a new document with the error fixed that again states **all** required items, or issue a document that makes its relation to the original clear and states the corrected items. The issuer keeps copies of both the original and the correction. The NTA's example of the first way is titled 請求書《修正》, so the new document shows that it is a correction. [NTA-QA-FULL 問32, 問33, PDF p.43 to 44] Alternatively, the buyer may make a corrected purchase statement (仕入明細書) and have the seller confirm it, and then the seller need not issue a correction. [NTA-QA-FULL 問32]

Discounts on last month's sales cannot simply be netted into this month's invoice. The issuer needs a return invoice for last month and a qualified invoice for this month. [NTA-LEAFLET PDF p.10]

## Retention

- **Issuer:** keep copies for **7 years**, counted from the day after two months have passed since the end of the taxable period in which they were issued. [NTA-LEAFLET PDF p.11]
- **Buyer:** keep the ledger and invoices for 7 years from the day after the filing deadline of the final return for that taxable period. [NTA-LEAFLET PDF p.13]

## Transitional measures (as revised April 2026)

These affect buyers and small issuers. The platform mainly needs them for reporting, not for issuing. [NTA-EN p.2-4]

- **Purchases from non-registered sellers** may still get a partial deduction: 80% (1 Oct 2023 to 30 Sep 2026), 70% (to 30 Sep 2028), 50% (to 30 Sep 2030), 30% (to 30 Sep 2031).
- **20% special rule (2割特例)** for businesses that became issuers when the system began: taxation periods including 1 Oct 2023 to 30 Sep 2026. Sole proprietors get a 30% rule in 2027 and 2028.
- **Small-amount rule:** businesses with taxable sales of ¥100 million or less (or ¥50 million or less for a specified period) can deduct purchases under ¥10,000 including tax with the ledger alone, from 1 Oct 2023 to 30 Sep 2029.

## Design implications

- **Store amounts in yen as integers.** JPY has no minor unit.
- **Tax engine:** group lines by rate, sum per rate, compute tax per rate, round once per rate with a rounding mode set per tenant. Test that per-line rounding is never used.
- **Validate registration numbers** as `T` + 13 digits. Checking a corporate number's check digit or querying the NTA announcement site is a follow-up (see open questions).
- **Document types:** qualified invoice, simplified qualified invoice, return invoice, corrected invoice. Issued documents are immutable; a correction is a new document that references the original.
- **Errors are fixed with a corrected invoice, not a return invoice.** A return invoice (適格返還請求書) is for returns, discounts, and rebates. A plain new invoice with no visible link to the original is not one of the NTA's correction methods, so the platform needs a corrected-invoice document that references the original and restates all items.
- **Retention clock** depends on the tenant's taxable period, not only the issue date.

## Open questions

- Does the corporate number have a check digit we can validate offline, and what is the algorithm? Find the primary source (NTA corporate number site).
- Does the NTA announcement site offer a public API for looking up registration numbers, and what are its terms?
- Japan has no mandatory structured e-invoice format. Is there a voluntary national standard (for example a Peppol profile) worth supporting? Needs a primary source before it goes into scope.

## Sources

- **[NTA-EN]** National Tax Agency, *Japan Invoice System Instructions* (Jun. 2024, revised Apr. 2026): https://www.nta.go.jp/taxes/shiraberu/zeimokubetsu/shohi/keigenzeiritsu/pdf/0024006-039_01.pdf
- **[NTA-LEAFLET]** 国税庁, 『インボイス制度の理解のために』 (令和8年4月): https://www.nta.go.jp/taxes/shiraberu/zeimokubetsu/shohi/keigenzeiritsu/pdf/0020006-027.pdf
- **[NTA-QA-57]** 国税庁 インボイスQ&A 問57 (適格請求書に記載する消費税額等の端数処理): https://www.nta.go.jp/taxes/shiraberu/zeimokubetsu/shohi/keigenzeiritsu/pdf/qa/57.pdf
- **[NTA-QA-28]** 国税庁 インボイスQ&A 問28 (少額な対価返還等に係る適格返還請求書の交付義務免除): https://www.nta.go.jp/taxes/shiraberu/zeimokubetsu/shohi/keigenzeiritsu/pdf/qa/28.pdf
- **[NTA-QA-FULL]** 国税庁『消費税の仕入税額控除制度における適格請求書等保存方式に関するＱ＆Ａ』(令和８年５月改訂), full version: https://www.nta.go.jp/taxes/shiraberu/zeimokubetsu/shohi/keigenzeiritsu/pdf/qa/01-01.pdf
  - SHA-256 of the copy read (downloaded 2026-11-01): `d0e50ed858d7ea7d707edd9dd440878740aeb8f6c9c4994a47f2f167699d3763`
