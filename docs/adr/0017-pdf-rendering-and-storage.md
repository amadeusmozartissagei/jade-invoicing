# 0017. Rendering invoice PDFs and storing them

- **Status:** Accepted for P1–P9 and D7–D8, which Hamza decided. The agent's choices in D1–D6 and D9–D11 are Proposed until he reviews them.
- **Date:** 2026-11-22 (spec), 2026-11-23 (design and implementation)
- **Decided by:** Hamza Pratama (owner) accepted every recommendation in three `grilling` rounds on 2026-11-22. The AI coding agent researched the facts and ran the spike.

## Context

Phase 1 slice (e) gives every issued document a PDF. ADR 0006 chose Typst, and its spike passed on 2026-11-22 (results in ADR 0006). The contract already has `GET /v1/invoices/{id}/files`, which returns an empty list until a file exists (ADR 0008, A8). The data model has `invoice_files` with a `sha256` per file. Facts and sources:

- [`docs/research/2026-11-22-pdf-storage-and-typst.md`](../research/2026-11-22-pdf-storage-and-typst.md) for Supabase Storage, Typst, and Vercel
- [`docs/domain/germany-e-invoicing.md`](../domain/germany-e-invoicing.md#document-titles-on-the-pdf) for the German document titles

## Decisions

### P1. When the PDF is rendered

**Asynchronously.** Issuing a document (and voiding, which issues a credit note) sends a `render_pdf` job to a `pgmq` queue in the same transaction, like the webhook outbox (ADR 0016). A worker renders the PDF, uploads it, and inserts the `invoice_files` row.

The worker is `POST /internal/queue/drain`, which `pg_cron` calls through `pg_net` (ADR 0003). The same endpoint will drain the webhook queue in slice (f).

**Alternatives.**

- Rendering inside the issue transaction would hold the number-series lock during render and upload, and a rollback after a successful upload would leave an orphaned file.
- Rendering lazily on the first `GET /files` would give a read request side effects and make the first call slow.

### P2. Failed renders

A job is tried at most 5 times, counted by `pgmq`'s `read_ct`, with a 60-second visibility timeout. After that it is archived and the error is logged, and `GET /files` stays empty for that document. A manual re-render endpoint can come later, if it is ever needed.

### P3. Which documents, and never twice

Every issued document gets a PDF: invoices, credit notes (including those created by void), and corrected invoices. A document that is later voided or corrected keeps its original PDF. A PDF is never rendered again, and `sha256` proves the archived file is unchanged.

### P4. Language and titles

**Language.** The PDF is in the tenant's jurisdiction's language, with no per-tenant choice in phase 1.

**Titles.** One title per document type, so the title follows from the type code alone:

| Document type | Japan | Germany |
|---|---|---|
| Invoice (380) | 適格請求書 | Rechnung |
| Credit note (381) | 適格返還請求書 | Rechnungskorrektur |
| Corrected invoice (384) | 修正した適格請求書 | Rechnungsberichtigung |

"Gutschrift" is never printed; in German VAT law it means self-billing.

**Reference to the original.** Every credit note and corrected invoice prints the preceding invoice's number and issue date. This meets "spezifisch und eindeutig" (§ 31 Abs. 5 UStDV) and the NTA's requirement that a correction shows its link to the original (問33).

### P5. Storage and downloads

- **Bucket.** A private Supabase Storage bucket, created by an SQL migration.
- **Credential.** The server uses Supabase's S3-protocol access keys, stored as a Vercel secret. They reach Storage only, not the database. The alternatives were rejected:
  - the service-role key bypasses RLS on the whole database
  - a JWT signing key can mint any role
- **Downloads.** `GET /files` returns SigV4-presigned URLs that expire after 5 minutes.
- **Open.** Whether the S3 protocol is enabled on the Free plan is unconfirmed. If it is not, the agent asks again before choosing another credential.

### P6. Shipping Typst, fonts, and templates

- **Typst.** v0.15.1 is downloaded at build time and checked against its SHA-256; it is not committed. The Vercel Go preset only allows this through `buildCommand`, which then also runs `go build -o "$VERCEL_OUTPUT_FILE"` (ADR 0006, spike results).
- **Fonts and templates.** They are committed under `apps/api`: Noto Sans and Noto Sans JP, Regular and Bold, about 10 MB, SIL OFL 1.1, with their license files.
- **Bundling.** All of them are embedded with `go:embed` and written to `/tmp` on first use.
- **Data, not markup.** Invoice data reaches a template only as JSON data, never as Typst source (ADR 0006).

### P7. The drain endpoint's token

The bearer token for `/internal/queue/drain` is kept on both sides:

- **In the database:** in Supabase Vault, read by the `pg_cron` job, so it never appears in `cron.job` text. This closes the follow-up in ADR 0003.
- **In Go:** in the env var `JADE_DRAIN_TOKEN`, compared in constant time.

Hamza sets both values himself through a `wizard` script, so the secret never passes through the agent.

### P8. If Typst had failed

The fallback was ADR 0006 option B, a pure-Go library, without a new decision. It was not needed: the spike passed.

### P9. PDF/A-3b from the start

Every PDF is rendered with `--pdf-standard a-3b`, the archival format the spike validated with veraPDF (ADR 0006). Phase 2 then only adds the XML attachment. Documents are never re-rendered (P3), so plain PDFs from phase 1 would stay non-archival for their whole retention period. Decided by Hamza on 2026-11-22.

## Design

Done with `codebase-design` on 2026-11-22. Hamza chose P9, D7 and D8; the agent proposed the rest.

### Modules

| Module | Interface | Behind it |
|---|---|---|
| `internal/invoicepdf` | `New(Options) (*Renderer, error)`; `(*Renderer).Render(ctx, Input) (PDF, error)`, where `PDF` holds the bytes and their SHA-256 | Builds a view model from `Input` (pure Go: titles, labels, number and date formatting per jurisdiction, the 8% mark, the preceding-invoice line), writes it as `data.json`, extracts the Typst binary, fonts and templates to a temporary directory once per process, runs Typst with a timeout, and reads the result |
| `internal/objstore` | `Put(ctx, key, body, contentType) error`; `PresignGet(ctx, key, ttl) (url string, err error)` | `aws-sdk-go-v2` S3 client against Supabase's S3 endpoint, path-style |
| `internal/worker` | `Drain(ctx) Report` on a `Drainer` configured with the pool, the queue handlers, the attempt limit and a time budget | `pgmq.read` per queue, dispatch by queue name, `pgmq.archive` on success, archive and log once `read_ct` passes the limit (P2), stop when the budget is spent. Holds the `render_pdf` handler; slice (f) adds the webhook handler |
| `httpserver` | `POST /internal/queue/drain` and `GET /v1/invoices/{id}/files` | Bearer check in constant time (P7), then `Drain`; the files list read under RLS and presigned per row |

`Input` is plain data (the issued invoice, its lines, tax breakdowns, the two frozen parties, the preceding invoice's number and issue date, the tenant's jurisdiction and currency), so `invoicepdf` does not import `store` and its tests need no database.

### D1. Enqueueing the job

`issueDraft` sends `pgmq.send('render_pdf', {tenant_id, invoice_id})` right after the document is marked issued, inside the issue transaction. Void and correct go through `issueDraft`, so credit notes and corrected invoices are covered (P3). The message carries IDs only, as for webhooks (ADR 0016).

### D2. The job, step by step

1. In one tenant transaction (`db.InTenantTx` with the message's `tenant_id`): if a `pdf` row already exists in `invoice_files`, the job is done. Otherwise read everything `Input` needs.
2. Render outside any transaction.
3. Upload to `<tenant_id>/<invoice_id>/pdf/<sha256>.pdf`.
4. In a second tenant transaction, insert the `invoice_files` row with `on conflict (invoice_id, kind) do nothing`.
5. Archive the message.

A crash between any two steps leaves the message in the queue, and the next attempt repeats the steps safely:

- The render is deterministic: identical input, pinned Typst and fonts, and `--creation-timestamp` set to the issue time. A retry produces the same bytes, so the upload rewrites the same object.
- The key contains the hash, so an object is never replaced by different content.
- The unique `(invoice_id, kind)` keeps the first row.

### D3. What the PDF prints

Only fields fixed at issue. `status` and `amount_paid` change after issue, so they are not printed. A render therefore depends only on immutable data, which makes D2's retries safe.

Beyond the required items, the PDF also prints:

- the due date, references, payment terms, the note, and the seller's bank account, when present
- unit codes as words (`HUR` as "Std." or 時間)
- the price base quantity, when it is not 1 ("10,50 / 100 Stk."), so that quantity × price reads as the line amount

### D4. The drain call

- **Batch and budget.** Each queue is read in batches of 5 with a 60 s visibility timeout (P2). A call starts no message after 15 s and gives each message at most 8 s, so it answers within 23 s: below the server's 30 s write timeout and `pg_net`'s 25 s. (The first draft said 20 s, checked per batch; review found that a batch of slow renders could run past both limits.)
- **Attempts.** A message whose `read_ct` is already above 5 is archived without being handled, so an attempt that died before it was counted cannot lead to a 6th render. After a failed 5th attempt the message is archived at once.
- **A queue whose handler cannot work is not read.** Without Storage or a renderer, `render_pdf` messages wait with all their attempts, instead of using them up within five minutes.
- **Response.** `200` with counts per queue (done, failed, given up). A missing or wrong token gets a `401` problem. The endpoint is internal and stays out of `openapi.yaml`; `docs/design/api.md` describes it.
- **Overlap.** Two overlapping calls never get the same message, because the visibility timeout hides it. A rare double run after a timeout is handled by D2.

### D5. The schedule

A `pg_cron` job runs every 10 seconds and calls `net.http_post` only when two things hold:

- Vault has both `drain_url` and `drain_token`. The local stack and CI have neither, so the job does nothing there.
- Some queue has a visible message.

An empty queue therefore costs no Vercel invocation. The request timeout is 25 s.

### D6. Bucket, queue, and grants in one migration

- **Bucket.** `insert into storage.buckets` creates `invoice-files`: private, `file_size_limit` 10 MB, `allowed_mime_types` `{application/pdf}` (phase 2 widens it). Uses `on conflict do nothing`.
- **Queue.** `pgmq.create('render_pdf')`, with the same grants to `jade_api` as `webhook_deliveries`.
- **Schedule.** The cron job from D5.

### D7. Storage in tests

Tests use real Supabase Storage, not a fake: locally and in CI, the stack starts Postgres, Storage, and Kong, and the other services are excluded. The `objstore` tests upload, presign, and download through the presigned URL. The `/files` tests download the PDF and compare its SHA-256. Decided by Hamza on 2026-11-22. CI gets about a minute slower.

### D8. S3 client

`aws-sdk-go-v2` (Apache-2.0) with `s3.PresignClient`. It is the common choice for this job. Decided by Hamza on 2026-11-22. Rejected: `minio-go`, which is smaller but less common, and hand-written SigV4, which is more code to own.

### D9. Finding Typst

- **On Vercel.** A build tag `typst_embedded` adds a file that embeds `bin/typst`. The build command is: fetch the pinned Linux binary, check its digest, then `go build -tags typst_embedded -o "$VERCEL_OUTPUT_FILE" ./cmd/api`.
- **Without the tag (local and CI).** The binary path comes from `JADE_TYPST_BIN` or `typst` on `PATH`, fetched by the same script, each pinned by its SHA-256.
- **When it is missing.** Render tests skip outside CI and fail in CI, following the `TEST_DATABASE_URL` convention.

Fonts and templates are always embedded (P6).

### D10. Configuration

| Variable | Use | Missing |
|---|---|---|
| `JADE_DRAIN_TOKEN` | Drain bearer token (P7) | `/internal/queue/drain` answers `401` to everyone; logged once at start |
| `JADE_S3_ENDPOINT`, `JADE_S3_REGION`, `JADE_S3_ACCESS_KEY_ID`, `JADE_S3_SECRET_ACCESS_KEY` | Storage (P5) | Drain leaves `render_pdf` messages in the queue; `/files` answers `500` only when a row exists to presign |

### D11. Japanese return invoices

A Japanese credit note labels its own supply date 返還等の年月日 (date of the return) and adds 元の取引年月日, the preceding invoice's supply date or period. The NTA lists both for a return invoice ([japan-qualified-invoice.md](../domain/japan-qualified-invoice.md#other-documents-the-issuer-must-handle)). The spec review found the gap; P4 alone did not cover it.

## Acceptance criteria

Each one gets a test. Hamza accepted them on 2026-11-23, with tests at three seams: `invoicepdf.Render`, `objstore`, and the HTTP endpoints.

1. Issuing an invoice, a credit note (including through void), or a corrected invoice enqueues exactly one `render_pdf` message, in the issue transaction. A failed issue enqueues nothing.
2. After a drain, the document has one `pdf` row whose `sha256` and `byte_size` match the stored object, and `GET /files` lists it with a URL that downloads exactly those bytes and expires 5 minutes after the response.
3. `GET /files` for a draft or a document without a PDF yet returns `200` with an empty list. Another tenant's document returns `404`.
4. A drain run twice, or after a crash between upload and insert, leaves exactly one row and an unchanged `sha256`. A document that already has a PDF is never rendered again.
5. A message that keeps failing is archived after its 5th read and never processed again. Messages after it in the same batch are still processed.
6. The drain endpoint answers `401` without a token, with a wrong token, and when `JADE_DRAIN_TOKEN` is unset.
7. Extracted PDF text contains the title from P4 for each jurisdiction and document type, and the preceding invoice's number and issue date on credit notes and corrected invoices.
8. A Japanese PDF contains every item of the qualified invoice (registration number, transaction date, the 8% mark, totals and tax per rate, the buyer's name). A German PDF contains every Pflichtangabe that § 14 Abs. 4 UStG asks for and the issue check enforces.
9. The PDF passes veraPDF as PDF/A-3b (in CI). Rendering the same input twice gives identical bytes.
10. Customer text containing Typst markup (`#`, `$`, `[`, `]`, `\`) appears literally and is never evaluated.
11. With the migration applied, the bucket is private: an unsigned GET of an object fails.

## Still to do

Steps only Hamza can do, through a `wizard` script written at the Ship step:

1. Push the migration (`supabase db push`).
2. Check that the S3 protocol is on for the Free project (still unconfirmed, P5). Create an S3 access key.
3. Set the Vercel env vars of D10, as secrets, for Production.
4. Put `drain_url` and `drain_token` in Vault.

## Consequences

- **Where the tests are.** Every criterion has a test:
  - 7–10 in `apps/api/internal/invoicepdf/invoicepdf_test.go`
  - 11 and the download part of 2 in `internal/objstore/objstore_test.go`
  - 1–6 and D5 in `internal/httpserver/files_test.go`

  They run the real Typst binary, the local Storage, and Postgres. PDF text is read back with `pdftotext`, and CI checks six sample PDFs, every type in both jurisdictions, with veraPDF.
- **Mutation check.** 22 mutations were tried.
  - 20 failed a test.
  - One test let its mutation through because the test was too weak: the Storage check in D10 ran without a renderer. The test was fixed, and the mutation now fails it.
  - One mutation could not change behaviour.
- **Review.** Both axes ran (`mattpocock-skills:code-review`), and the fixes went through the verify step again.
  - The Standards axis found no breach of a documented rule. It found five bugs, all fixed:
    - the drain budget was checked only per batch (D4)
    - a failed asset setup stuck until the next cold start
    - Typst's full stderr went into logs; now only its first line goes there
    - `Close` was not locked
    - the `.gitignore` entries were anchored at the wrong directory
  - The Spec axis found gaps, all fixed:
    - no test for a crash between upload and insert
    - attempts were counted only after handling (D4)
    - messages lost their attempts when Storage was missing (D4)
    - the Japanese return invoice lacked the original transaction's date (D11)
    - `price_base_quantity` was not printed (D3)
    - the D5 function had no test
    - the veraPDF samples covered only three documents
  - Kept: test objects stay in the local bucket.
- **What slice (f) can reuse.** `internal/worker` drains any queue. The webhook handler adds one `worker.Queue` to the drain endpoint and one `exists` line to `app.request_queue_drain`.

**Follow-ups:**

- **Nothing is deployed yet.** The steps under "Still to do" come first. Until then, production renders nothing: `render_pdf` messages wait in the queue.
- **Is the S3 protocol on for Free?** If it is not, the agent asks before choosing another credential (P5).
- **Tracing a lost PDF.** A document whose render was given up has no file and only a log line. A manual re-render endpoint can come later (P2).
- **Phase 2.** It adds the XML attachment and the ZUGFeRD XMP block (ADR 0006), and widens the bucket's allowed MIME types.
