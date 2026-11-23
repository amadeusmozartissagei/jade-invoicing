# Interview notes: phase 1 (core flow)

Likely questions about phase 1 of jade-invoicing, with short answers in plain language. Each answer names the document that has the full reasoning. The design quiz, held once development is done, revises any answer that turns out shaky.

## Tenants, API keys, and request validation (slice 7a)

**How does a client company get access?**
In phase 1 an operator runs `cmd/admin create-tenant`, which connects as `postgres` and, in one transaction, creates the tenant, an empty seller profile, the first API key, and an audit entry. It prints the key once. Self-service signup comes with the dashboard (ADR 0012).

**What does a key look like, and what do you store?**
`jade_sk_<prefix>_<secret>`. The prefix is 10 random bytes and is public: it is how the key is looked up. The secret is 32 random bytes; only its SHA-256 is stored. Both parts are base32 in lowercase, so there is no `_` inside a part and the key splits unambiguously (ADR 0012).

*Why SHA-256 and not bcrypt?* bcrypt is for passwords, which people choose and which can be guessed. A 256-bit random secret cannot be brute-forced, so a slow hash would only add latency to every request (data-model.md).

**Walk me through authenticating a request.**
1. Parse the `Authorization: Bearer` header against a strict format. A malformed key is rejected without touching the database.
2. Look the prefix up with a `security definer` function. It is needed because row-level security hides keys until the tenant is known.
3. Hash the presented secret and compare it with the stored hash in constant time.
4. Reject expired and revoked keys with exactly the same `401` as unknown ones.
5. Open a transaction, set the tenant for RLS, check that the tenant is not suspended (`403`), record the key's use at most once a minute, and run the handler in that same transaction.

**Why is every rejected key the same 401?**
If an unknown key and a revoked key gave different answers, an attacker could learn which keys exist. A test sends malformed, unknown, wrong-secret, expired, and revoked keys and checks that the responses are byte-identical apart from the request ID (api.md, criterion 9).

*Is the constant-time compare enough to stop timing attacks?* It stops leaking how many bytes of the hash match. Whether a prefix exists could still show in timing, because a found row is a little different from no row; an unknown prefix is compared against a dummy hash to keep the work the same. The prefix is not a secret on its own, so this is defence in depth, not the main protection. The main protection is the 256-bit secret.

**Why does the API buffer the response?**
So it can commit before it answers. If the handler wrote `200` straight to the client and the commit then failed, the client would believe a change was saved when it was not. Now a failed commit becomes a `500`. A test kills the database session in the middle of a request and checks for the `500` (ADR 0012). The idempotency keys in the next slice depend on this: the stored response and the change commit together.

**What happens to a request that fails validation?**
The transaction rolls back, so nothing changes, not even the key's `last_used_at`. Every `4xx` and `5xx` rolls back.

**How is the request validated?**
The contract (`openapi.yaml`) is embedded in the binary and checked with `libopenapi-validator`, after authentication. The rules:
- A wrong `Content-Type` gets `415`.
- Broken JSON gets `400`.
- A body that breaks the schema gets `422` with one entry per field. Each entry has a JSON Pointer such as `/lines/0/quantity` and a code from `errors.md`.

The spec checks structure. Domain rules, such as currency decimals, are checked in Go.

**What was the "unevaluatedProperties false positive"?**
The schemas use `unevaluatedProperties: false` to reject unknown fields. In JSON Schema 2020-12, when one field fails, the subschema that declares the other fields produces no annotations either, so the validator reports the *valid* fields as unevaluated too. Mapped naively, `{"legal_name": 5, "city": "Berlin"}` would tell the client that `city` is an unknown field. The fix ignores the library's unknown-field reports and walks the body against the schema's declared properties. A test covers the mixed case, and fails if the fix is removed (ADR 0010, ADR 0012).

**Why validate after authentication and not before?**
So an unauthenticated caller learns nothing about the request format, and the validation cost is only paid for real clients. The price is one database lookup for an invalid request from a real client, which is small.

**What fails if the database URL is missing, or the database is paused?**
`/healthz` still answers, because it never touches the database and the pool connects lazily. `/v1` answers `500`, and the log says why. On Supabase Free a paused project means every API call fails until it is resumed, which is why there is a keep-alive job (next section).

**What if the database hangs instead of failing?**
Each request's database work has a 10-second deadline. After that the transaction is cancelled and the client gets a `500` problem, instead of waiting until the platform kills the function.

**How do you know the tests test anything?**
Each important check was removed on purpose, one at a time, and its test failed:
- the hash compare
- the expiry and revocation checks
- the suspension check
- the rollback on error
- commit-before-send
- the unknown-field fix
- the UTF-8-safe truncation

The database tests run as `jade_api` under RLS, the same as production.

## Keeping the Supabase Free project awake (ADR 0013)

**What happened?**
The Free project paused after about a week without work, and every `/v1` call failed. It was restored through the Supabase Management API in about four minutes, with no data lost.

**Why not just ping `/healthz`?**
Supabase decides on "user database activity", not HTTP traffic, and `/healthz` deliberately never touches the database. So there is a separate `/readyz` that runs one query as the API's own role through the pooler.

**Why two endpoints?**
They answer different questions. `/healthz` says the process is up, so it stays green while the database is down and you can tell the two failures apart. `/readyz` says the API can actually serve requests. Kubernetes uses the same split between liveness and readiness.

**Why GitHub Actions and not `pg_cron` or Vercel cron?**
Supabase does not say whether its own scheduled jobs count as user activity. Vercel cron would watch Vercel from inside Vercel. A GitHub schedule goes through the same path as a real client, and a failed run emails the owner, so it is also a free outage alert.

**What are its weak points?**
- GitHub can delay or drop scheduled runs, so it runs twice a day for a seven-day window.
- GitHub disables schedules after 60 days without commits in a public repository.
- `select 1` might not be "enough" activity. Supabase emails a warning a week before pausing, and the fallback is a heartbeat write.


## Customers and idempotency keys (slice 7b, ADR 0014)

**What problem do idempotency keys solve?**
A client sends `POST /v1/customers`, the network times out, and it cannot tell whether the customer was created. With an `Idempotency-Key` it simply retries: if the first request committed, it gets the same response back (marked `Idempotent-Replayed: true`) and nothing is created twice.

**Where is the stored response written, and why there?**
In the same database transaction as the customer itself. Either both commit or neither does. There is no "in progress" row a crash could leave behind, so no recovery job is needed.

**What happens if two retries arrive at the same moment?**
The first takes a Postgres advisory lock on (tenant, key) that lasts until its transaction ends. The second tries the lock without waiting, fails, and gets `409` with `Retry-After: 1`. When it retries after the first committed, it gets the replay. The lock is transaction-scoped, so it works behind Supabase's transaction pooler.

**What did the review find about that?**
Every request also records "last used" on its API key row. The first request in each minute held that row lock until commit, so a second request with the same key queued behind it instead of getting the `409`. The update now uses `for update skip locked`: if the row is busy, it skips the touch. A test holds the row lock and checks that a request still answers at once.

**Why not store error responses too?**
A failed request changed nothing. Not storing it lets the client fix the problem (say, a duplicate `external_ref`) and retry with the same key. The IETF draft leaves this choice to the server.

**What if the same key comes with a different body?**
The fingerprint is a SHA-256 over method, path, and body. A mismatch is `422 idempotency_key_reused`, because it is almost always a client bug: reusing a key for a new request.

**How long are keys kept, and why does it matter for GDPR?**
24 hours. The stored body of a created customer contains personal data, so a daily `pg_cron` job deletes expired rows. An expired key is treated as new.

**How is customer personal data protected now, before encryption?**
- It is never written to logs; a test checks that.
- The audit log records which fields changed, never the values.
- The only list filter is `external_ref`, which is not personal data, so encrypting personal columns in phase 4 will not break any query.
- RLS keeps every tenant's customers invisible to other tenants.

**Why keyset pagination?**
Page 1,000 costs the same as page 1, and new rows do not shift the pages. The cursor carries the last row's `(created_at, id)` and a hash of the filters, so a cursor reused with other filters is rejected instead of returning a wrong page. A test pages through 250 customers, most of them with the same `created_at`, and gets each exactly once.

**Why no `If-Match` on customer `PATCH`?**
The contract makes it last write wins. Issued invoices copy the customer's details at issue, so editing a customer never changes an issued document. Drafts of invoices do use `If-Match`.


## Invoice drafts and the tax engine (slice 7c, ADR 0015)

**How is VAT calculated, and why not per line?**
Each line's net amount is rounded to the currency unit. Lines are grouped by rate, and the tax is computed once per group from the group's total. Both countries require that: EN 16931 computes tax per VAT breakdown, and Japan's NTA explicitly forbids rounding per line and summing. Example: five lines of €0.03 at 19%. Per group: 0.15 × 19% = 0.0285, which rounds to €0.03. Per line it would be 5 × €0.01 = €0.05.

**How do Germany and Japan differ in the engine?**
Only in two parameters:

- **Minor units:** EUR has 2, JPY has 0.
- **Tax rounding:** Germany rounds commercially (half away from zero); in Japan the issuer chooses down, up, or half up.

The engine is one pure function with no database access.

**How do you know the arithmetic is right?**
- **Fixed cases** with exact expected amounts, including exact half cents.
- **An oracle test.** 2,000 random invoices are compared with a second implementation written with Go's `math/big` rationals, so the test does not check the decimal library against itself.
- **Mutation testing.** Each rule was broken on purpose, and a test failed every time.
- **No floats anywhere.** Money is a decimal string in JSON, `numeric` in Postgres, and `shopspring/decimal` in Go.

**What subtle bug did you avoid in the division?**
Dividing by 100 with the library's `Div` rounds at 16 decimals. `Shift(-2)` moves the decimal point and is exact, so the tax rounding sees the true value.

**What does `If-Match` protect against?**
Lost updates. Two people load the same draft (version 3) and both save. Without a check, the second save silently overwrites the first. With `If-Match: "3"` the server locks the row, compares the version, and lets only the first save through; the second gets `412` and must re-read. A test fires eight concurrent edits with the same ETag: exactly one wins.

**Why is a missing `If-Match` a 428 and not a 400?**
RFC 6585 defines `428 Precondition Required` for exactly this case. The check runs before request validation, which would otherwise treat a missing required header as a generic `400`.

**Why check the tax rate at draft time if issue checks it again?**
So that a draft never shows totals at a rate that is not valid. Without a supply date yet, the draft is checked against today in the tenant's time zone. Issue checks again against the final supply date.

**How do you insert 500 lines efficiently?**
In one statement. The lines go to Postgres as a JSON array and `jsonb_to_recordset` turns them into rows. Amounts stay strings until Postgres casts them to `numeric`, so they are never floats. `unnest` over twelve arrays would also work, but `sqlc` cannot analyse it.


## Issuing, voiding, correcting, and the outbox (slice 7d, ADR 0016)

**How do you guarantee invoice numbers have no gaps and no duplicates?**
- **The counter.** The number comes from a counter row that is incremented inside the issue transaction. The row stays locked until commit, so concurrent issues on one series wait their turn.
- **Rollback.** If anything fails afterwards, the whole transaction rolls back, the increment included, so no number is lost.
- **Why not a Postgres sequence.** It is not per tenant, and it leaves gaps on rollback.
- **The test.** 20 concurrent issues get exactly 1 to 20.

**Why take the number last?**
Every issue on one series waits on that counter row. Doing the checks and calculations first, and taking the number just before marking the invoice issued, keeps the wait short.

**Can two transactions deadlock?**
Every path locks invoice rows first, in id order, and the number series last. With one global lock order there can be no cycle.

**What does "incomplete" mean, and why those fields?**
Only what the law requires. In Germany, § 14 Abs. 4 UStG requires:

- the full name and address of both parties
- the seller's VAT ID or tax number
- a supply date
- the line items

In Japan, the qualified invoice requires the issuer's name and registration number, the transaction date, and the recipient's name.

Everything missing comes back at once, with a pointer to each field. XRechnung's extra fields (contact, IBAN) are checked in phase 2, when the XML exists.

**Why does void create a credit note instead of flipping a status?**
Both countries correct issued invoices with a new document that refers to the original, so the buyer and the tax records see the cancellation. The credit note copies the original's lines and tax rounding, so it cancels it exactly. It also copies the original's parties, so later edits to the customer cannot make the void fail or change who it names.

**What is a transactional outbox, and why use one?**
When an invoice is issued, the event row, one delivery row per subscribed webhook endpoint, and a queue message per delivery are written in the same transaction as the invoice. Either the invoice and its events both exist, or neither does. There is no window where the invoice is issued but the webhook was never queued, and no message for an invoice that rolled back. The queue (`pgmq`) lives in the same Postgres, which is what makes that possible.

**Why do queue messages carry only IDs?**
The queue tables have no row-level security. A message holds a delivery ID and a tenant ID; the worker reads the rest under that tenant's RLS. The webhook payload itself has no personal data either, so a misconfigured endpoint leaks nothing personal.

**How did testing find a redundant check?**
Mutation testing: each rule was broken on purpose to see whether a test fails. Removing void's own "already has credit notes" check failed nothing, because the general credit-limit rule at issue already refuses it. The duplicate was deleted, leaving one rule in one place.

## Invoice PDFs (slice 7e, ADR 0006, ADR 0017)

**Why render the PDF in the background, not while issuing?**
- **The lock.** Issuing holds the number-series lock. Rendering and uploading inside that transaction would make every other issue on the series wait for Typst and the network.
- **Rollback.** A rollback after a successful upload would leave a file for an invoice that was never issued.
- **How.** The issue transaction only queues a message (`pgmq.send`). It commits or rolls back together with the invoice.

**How does the background job run without a server that runs all the time?**
- **The schedule.** `pg_cron` in the database runs every 10 seconds.
- **The call.** When a message is waiting, it calls the API's drain endpoint through `pg_net`, with a bearer token kept in Supabase Vault.
- **No work, no call.** An empty queue costs no Vercel invocation.

**What if the job crashes halfway, or runs twice?**
- **Every step can repeat.**
  - The render is deterministic: same data, pinned Typst and fonts, and the PDF's creation date set to the issue time. A retry makes the same bytes.
  - The file is stored under its SHA-256, so a retry overwrites it with identical content.
  - The database row is unique per document and kind. The first one wins.
- **The tests.** One test makes the insert fail after the upload, then shows the retry ends with one row matching the stored file.

**What happens to a job that keeps failing?**
- **Retries.** pgmq counts reads (`read_ct`). A failed job becomes visible again after 60 seconds.
- **Giving up.** After the 5th attempt the job is archived and logged. A message read more than 5 times is archived without running, in case an attempt died before it could be counted.
- **One bad job does not block the rest.** A test puts a broken message first in the queue and checks that the good one behind it still runs.

**How do customers download the PDF without the bucket being public?**
- **Presigned URLs.** The bucket is private. `GET /files` returns presigned S3 URLs (SigV4) that expire after 5 minutes. The signature is computed locally, so listing files makes no call to Storage.
- **The key.** The server's S3 key works only in Storage, not in the database. That is why it was chosen over Supabase's service-role key, which bypasses row-level security everywhere.

**Can a customer name break the PDF or run code in it?**
- **Data, never markup.** Invoice data reaches Typst only as a JSON file, and the template shows each value as text.
- **The test.** A buyer named `#panic("x") $x^2$ [*b*]` and a line called `#read("/etc/passwd")` both appear on the PDF exactly as typed.

**How do you know the PDF meets the law?**
- **Reading the PDF back.** The tests extract the text with `pdftotext` and check the required items:
  - in Japan, the registration number, transaction date, the ※ reduced-rate mark, and totals and tax per rate
  - in Germany, both addresses, the VAT ID or tax number, the dates, the number, and net and tax per rate
- **Archival format.** CI checks six sample PDFs, every document type in both countries, with veraPDF for PDF/A-3b.
- **What review found.** The spec review noticed that a Japanese return invoice must also show the original transaction's date. That is now printed.

**Why ship a 76 MB binary on a serverless platform?**
- **Fit.** Typst renders an invoice in about 50 ms and writes PDF/A-3. Phase 2 needs PDF/A-3 for ZUGFeRD.
- **How it gets there.** The Vercel Go preset bundles only the server binary, so Typst is embedded with `go:embed`. It is downloaded at build time and checked against its SHA-256.
- **Cost.** The spike measured no noticeable cold-start cost.
