# jade-invoicing

A platform that issues invoices following Japanese and German invoice rules, on behalf of client companies that integrate it through an API.

## Parties

**Tenant**:
A client company of the platform; it issues documents to its own customers and belongs to exactly one jurisdiction.
_Avoid_: Client, account, organization

**Seller profile**:
The tenant's own details as they will appear on its documents (legal name, address, tax identifiers, registration number).
_Avoid_: Company profile, issuer settings

**Customer**:
A business or individual the tenant issues documents to.
_Avoid_: Client, account, recipient

**Party**:
The seller or the buyer as recorded on one issued document, frozen at the moment of issue.
_Avoid_: Snapshot, contact

**Jurisdiction**:
The country whose invoice rules a tenant follows: Germany (DE) or Japan (JP).
_Avoid_: Region, locale, country setting

## Documents

**Document**:
Any of the three things the platform issues: an invoice, a credit note, or a corrected invoice. The API names its resource for all three "invoices", and its `document_type` field tells them apart.
_Avoid_: Invoice (when all three are meant), bill

**Invoice**:
A document that requests payment for supplies (UNTDID 1001 code 380); in Japan a qualified invoice, 適格請求書.
_Avoid_: Bill, receipt

**Credit note**:
A document from the seller that reduces an earlier invoice, fully or in part (code 381); in Japan a return invoice, 適格返還請求書.
_Avoid_: Gutschrift (that word means self-billing in German VAT law), refund, return

**Corrected invoice**:
A document that replaces an issued invoice with an error, restating all its items and referring to it (code 384); in Japan 修正した適格請求書.
_Avoid_: Amended invoice, reissue

**Preceding invoice**:
The issued invoice or corrected invoice that a credit note or corrected invoice refers to.
_Avoid_: Original (when ambiguous), parent

**Line**:
One item on a document: what was supplied, how much, at what net price and tax rate.
_Avoid_: Item row, position

## Lifecycle

**Draft**:
A document that has no number and no legal effect, and can still be edited or deleted.
_Avoid_: Pending, unsaved invoice

**Issue**:
The act that gives a draft its number and legal effect and freezes it for good.
_Avoid_: Send, finalize, publish

**Void**:
Cancelling an issued invoice in full by issuing a credit note for its whole amount.
_Avoid_: Cancel, delete, storno

**Correct**:
Replacing an issued invoice by issuing a corrected invoice that refers to it.
_Avoid_: Edit, amend

**Number series**:
A tenant's sequence of document numbers with a fixed prefix, which never skips or repeats a number.
_Avoid_: Counter, sequence, numbering scheme

## Money and tax

**Supply date**:
The date the goods or services were supplied, or the period they were supplied over; in Japan 取引年月日.
_Avoid_: Delivery date, service date, transaction date

**Issue date**:
The date a document was issued, in the tenant's time zone.
_Avoid_: Invoice date, created date

**Tax breakdown**:
A document's net total and tax for one tax category and rate; tax is computed once per breakdown, never per line.
_Avoid_: Tax summary, VAT lines

**Tax rounding**:
How a breakdown's tax is rounded to the currency unit: commercially in Germany, by the tenant's choice of down, up, or half up in Japan.
_Avoid_: Rounding mode (when line amounts are meant)

**Prepaid amount**:
Advance payments received before issue and deducted from the amount due.
_Avoid_: Deposit, down payment

**Payment**:
Money received against an issued invoice after issue; a document is paid when its payments reach the amount due.
_Avoid_: Settlement, receipt

**Retention period**:
How long an issued document must be kept: 8 years from the end of the issue year in Germany, 7 years from two months after the fiscal year end in Japan.
_Avoid_: Archive period, TTL

## Output

**Document file**:
A rendered, archived file of an issued document, such as its PDF; it is never rendered again.
_Avoid_: Attachment, export, printout

**Event**:
A record that something happened to a document (issued, voided, corrected, paid), sent to the tenant by webhook.
_Avoid_: Notification, message, callback

**Webhook endpoint**:
A URL of the tenant's that receives events.
_Avoid_: Callback URL, hook, subscriber

**Delivery**:
The sending of one event to one webhook endpoint, retries included.
_Avoid_: Notification, send, message
