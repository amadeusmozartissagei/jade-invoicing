# API error types

Every error response is an RFC 9457 problem details object (`application/problem+json`) ([design](../design/api.md#errors-a2), ADR 0008):

| Member | Meaning |
|---|---|
| `type` | The problem type's identifier: this page's URL with the code as fragment, `https://github.com/amadeusmozartissagei/jade-invoicing/blob/main/docs/api/errors.md#<code>`. RFC 9457 makes `type` the primary identifier, so clients may compare it directly |
| `code` | The same identifier without the URL (`invoice_not_draft`), for logs and `switch` statements. It always matches the fragment of `type` |
| `title` | The type's short summary, listed below; the same for every occurrence |
| `status` | The HTTP status code, repeated |
| `detail` | What went wrong in this request, for a human. Never branch on it |
| `request_id` | The same as the `Request-Id` header; quote it when reporting a problem |
| `errors` | For `validation_failed` and `invoice_incomplete`: one entry per field, see [Field errors](#field-errors) |

`type` values never change within `/v1`, even if the URL stops resolving. New types may be added without a new API version; handle an unknown one by its HTTP status.

**409 or 422?** Following RFC 9110: `422` means the request content is well-formed but wrong, and must be changed. `409` means the request is fine, or has no content, but the current state of the resources it acts on does not allow it; the `detail` (and `errors`, where present) says what to change first.

## Types

### `bad_request`

`400` · **Bad request.** The request cannot be read: invalid JSON, a query parameter of the wrong type, or a cursor that is malformed or was issued for other filters.

### `idempotency_key_required`

`400` · **Idempotency key required.** A `POST` without an `Idempotency-Key` header. Every `POST` needs one; a UUID v4 is recommended.

### `unauthorized`

`401` · **Unauthorized.** Sent with `WWW-Authenticate: Bearer`. The API key is missing, malformed, unknown, expired, or revoked; the response is the same in every case.

### `forbidden`

`403` · **Forbidden.** The tenant is suspended.

### `not_found`

`404` · **Not found.** No such resource for this tenant. IDs belonging to another tenant give this error too.

### `method_not_allowed`

`405` · **Method not allowed.** Sent with an `Allow` header. The path exists but not for this method.

### `invoice_not_draft`

`409` · **Invoice is not a draft.** The request needs a draft (edit, delete, issue), and the document is already issued, voided, or corrected. Correct an issued invoice with a credit note or a corrected invoice.

### `invoice_not_issued`

`409` · **Invoice is not issued.** The request needs an issued document: voiding it, or a credit note or corrected invoice referring to it. The document is a draft, a credit note, or voided or corrected.

### `invoice_incomplete`

`409` · **Invoice cannot be issued yet.** The draft, the seller profile, or the customer lacks data needed to issue. `errors` lists everything missing (see [Field errors](#field-errors)); complete it and issue again.

### `credit_exceeds_invoice`

`409` · **Credit exceeds invoice.** Issuing this credit note would make all credit notes for the invoice exceed its `tax_inclusive_total`. Reduce the credit note's lines.

### `correction_not_allowed`

`409` · **Correction not allowed.** The original cannot be corrected: it already has payments or credit notes (not supported in phase 1), or another correction of it has been issued.

### `payment_not_allowed`

`409` · **Payment not allowed.** Payments can only be recorded on an issued `invoice` or `corrected_invoice`, not on a draft, a credit note, or a voided or corrected document.

### `conflict_external_ref`

`409` · **External reference already used.** Another customer of this tenant has the same `external_ref`.

### `conflict_series_code`

`409` · **Series code already used.** The tenant already has a number series with this `code`.

### `cannot_revoke_current_key`

`409` · **Cannot revoke the current key.** The request tries to revoke the API key it was made with. Use another key, so the tenant does not lock itself out by mistake.

### `delivery_not_dead`

`409` · **Delivery is not dead.** Only `dead` deliveries can be redelivered; this one is `pending` or `succeeded`.

### `endpoint_disabled`

`409` · **Endpoint is disabled.** A delivery cannot be redelivered while its webhook endpoint is disabled. Enable the endpoint first.

### `idempotency_key_in_use`

`409` · **Idempotency key in use.** Sent with `Retry-After`. Another request with the same `Idempotency-Key` is being processed right now. Retry after the given number of seconds to get that request's result.

### `precondition_failed`

`412` · **Precondition failed.** The `If-Match` value is not the draft's current `ETag`: someone changed it since you read it. Read it again, reapply your change, and send the new `ETag`.

### `content_too_large`

`413` · **Content too large.** The request body is over 1 MiB.

### `unsupported_media_type`

`415` · **Unsupported media type.** Use `application/json`, or `application/merge-patch+json` for `PATCH`.

### `validation_failed`

`422` · **Validation failed.** One or more fields of the request are invalid. `errors` lists each one.

### `idempotency_key_reused`

`422` · **Idempotency key reused.** The `Idempotency-Key` was already used for a different request (method, path, or body). Use a new key for a new request.

### `precondition_required`

`428` · **Precondition required.** `PATCH` and `DELETE` of a draft need an `If-Match` header with the draft's `ETag`.

### `internal_error`

`500` · **Internal error.** Something failed on the server. The response contains no details; quote the `request_id`. A `POST` that failed this way stored nothing, so it is safe to retry with the same `Idempotency-Key`.

## Field errors

Each entry in `errors` has:

- `pointer`: a JSON Pointer (RFC 6901). For `validation_failed` it points into the request body (`/lines/0/quantity`). For `invoice_incomplete` it points into the draft (`/supply_date`), or into the seller profile or customer with a prefix (`/seller_profile/city`, `/customer/post_code`).
- `code`: one of the codes below.
- `detail`: a human-readable explanation.

| Code | Meaning |
|---|---|
| `required` | Missing (in the request, or missing to issue) |
| `unknown_field` | The request has a field this endpoint does not accept. Unknown fields are rejected, not ignored, so a misspelled field cannot be lost silently |
| `invalid_format` | Wrong type or format (a number instead of a decimal string, a malformed date, UUID, or email) |
| `invalid_value` | Not one of the allowed values |
| `too_long` | Longer than the maximum length |
| `too_many_items` | More items than allowed (for example over 500 lines) |
| `out_of_range` | Outside the allowed range (for example `limit`, `padding`) |
| `must_be_positive` | Must be greater than 0 |
| `too_many_decimals` | More decimals than the currency allows (EUR 2, JPY 0), or than 6 for quantities and prices. Amounts are rejected, never rounded |
| `rate_not_valid` | The tax rate is not valid for the jurisdiction on the supply date |
| `not_allowed` | The field is not allowed here (for example `preceding_invoice_id` on a plain invoice, or setting a non-nullable field to `null`) |
| `must_match_preceding` | Must equal the preceding invoice's value (for example `customer_id` of a credit note) |
| `url_not_allowed` | Webhook URL is not `https://` on port 443 |
| `invalid_period` | A period's end is before its start |
