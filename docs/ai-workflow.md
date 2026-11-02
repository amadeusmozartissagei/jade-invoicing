# How the AI coding agent is used

This project is built with an AI coding agent (Claude Code). The agent writes code and documentation and does research. The owner, Hamza Pratama, sets the goals, makes the decisions, reviews the work, and must be able to explain every part.

This file records what the agent did, what the human decided, and how the work was checked. Add an entry for each significant decision or slice.

## How each slice is reviewed

One model playing several roles is not an independent review, so the review steps are separated by who or what does them:

1. **Self-review by the authoring session.** Re-check every factual claim against its source, test cheap assumptions, and walk through the checklists below. Useful, but not independent.
2. **Tests and linters in CI.** The only check that does not depend on anyone's judgement.
3. **`/code-review`** on the diff before each commit or PR, run as a separate review pass rather than by the session that wrote the code.
4. **`/security-review`** on any slice that touches authentication, API keys, tenant isolation, webhooks, or personal data.
5. **Owner review.** Hamza reads the diff and must be able to explain it; at the end of each phase he is quizzed on the design.

Parallel work runs in separate git worktrees, one session per set of files.

An AI reviewing AI-written code can share its blind spots. Steps 2 and 5 are the checks that do not come from the model.

### Checklists

**Security**

- Every endpoint authenticates, and every query that reads tenant data runs inside a transaction that sets the tenant, so RLS applies.
- SQL values only go through parameters; no SQL is built from strings.
- No secrets in code, logs, URLs, or error messages; secrets come from the environment.
- Signatures (webhook HMAC, API keys) are compared in constant time; API keys are stored hashed.
- Input is validated at the API boundary; error responses do not leak internals.
- Personal data is kept to what the feature needs and is marked for later encryption and retention rules.

**Database**

- The change is a migration in `supabase/migrations/`, never a dashboard edit.
- New tables have RLS enabled and a policy; constraints (`not null`, `check`, foreign keys) encode the invariants.
- Issued invoices cannot be changed, enforced in the database, not only in Go.
- Concurrent writes (invoice numbering, status changes) have a stated locking strategy and a test.
- New queries have the indexes they need, and work through the transaction pooler.
- Money is never stored or computed as a float.

**QA**

- Every acceptance criterion has a test.
- Tax and rounding tests cover boundaries (exact half cents and yen, zero, multiple rates, credit notes) and cite the rule they check.
- Retries and duplicate requests are tested for idempotency.
- Test data is synthetic: no real companies, tax numbers, or people.

**Docs**

- Regulatory claims link a primary source; decisions have an ADR; `docs/interview-notes/` covers the slice.

## Decision log

### 2026-11-01: Pivot from e-KYC to invoicing (ADR 0001)

- **What the agent did.** It summarised the original project rationale, proposed a free-tier stack, compared domains, and checked the current free-tier limits of Vercel, Supabase, and Hugging Face against their official documentation.
- **What the human decided.** Hamza added the constraint that the deployment must be fully free. He also questioned whether e-KYC was still the right domain under that constraint, and chose the invoicing platform.
- **How it was checked.** The agent first claimed, from memory, that Hugging Face Spaces could host the Python worker for free. When asked to verify, it found in the official documentation that Docker Spaces now require a paid plan, and it corrected the claim. Every limit in ADR 0001 links to its source, and facts that could not be confirmed (for example, whether Supabase Free includes `pgmq` and `pg_cron`) are marked as open items for a spike.
- **Lesson.** Free-tier limits and platform policies change often. The agent's memory is not a source; official documentation with a retrieval date is.

### 2026-11-01: Domain research (docs/domain/)

- **What the agent did.** It read primary sources (NTA publications and Q&A, German statutes on gesetze-im-internet.de, KoSIT and FeRD pages, the GDPR) and wrote one note per topic, with a source for each claim and a list of design implications.
- **What the human decided.** Hamza chose to do the regulatory research before the free-tier spike.
- **How it was checked.** Page references to NTA PDFs were matched to the extracted text page by page. Claims the agent could not source were removed or moved to "open questions". The BMF website blocked automated access with a bot check. The agent did not try to pass the check; it read the BMF letter from a Wayback Machine snapshot instead, recorded the snapshot URL and SHA-256, and replaced the claims that had relied on a secondary summary (IHK Köln) with citations to the letter. One of those secondary claims was imprecise and was corrected.

### 2026-11-01: Reading the BMF letters

- **What the agent did.** It found a copy of the 2024 BMF letter hosted by a public-law chamber and recorded its SHA-256, and it found that a third BMF letter (15 Oct 2026) exists. It added both to the German domain note.
- **Where it stopped.** The BMF website shows a bot check. The agent did not click through it. One attempt to download the 2026 letter directly from the BMF site was blocked by the agent's own permission system as possible bot-check circumvention, and the agent stopped trying. The owner downloads such documents by hand.
- **Follow-up.** Hamza downloaded the 2026 letter by hand. The agent read it in full, replaced the secondary summary with citations to the letter, and recorded the file's SHA-256.
- **Mapping table.** The agent downloaded the KoSIT/FeRD mapping table (mandatory items to EN 16931 fields), checked the text extraction against the rendered pages where the columns had been mixed up, and summarised it in its own words in `docs/domain/germany-en16931-mapping.md`. The BMF letter PDF was moved into `docs/domain/sources/` at Hamza's request; the KoSIT/FeRD PDF was not copied, because it is copyrighted by its publishers.

### 2026-11-01: Free-tier spike (ADR 0003)

- **What the agent did.** It wrote the throwaway spike (Go functions, a small SQL runner, cron and queue SQL), deployed it to Vercel, ran the measurements, cleaned up the cron job and test queue, and wrote ADR 0003.
- **What the human decided.** Hamza approved the spike spec, created the Supabase and Vercel accounts, ran every login and `supabase link`, and put the database connection string into `.env.local` himself. The agent never saw the password: the SQL runner and the Vercel env setup read `.env.local` without printing it, and the spike token was generated straight into that file.
- **How it was checked.** Each acceptance criterion was tested against the live services: extension versions queried from the database, queue round trip, HTTP 401 without a token, cron run history, `pg_net` response codes, and end-to-end lag. What was not measured (cold start, Active CPU per call) is stated as such in the ADR.

### 2026-11-01: Layout, Go stack, and PDF engine (ADRs 0004 to 0006)

- **What the agent did.** It wrote the ADR template, checked the current Vercel Go runtime, Services, monorepo, and limits pages, and the Typst, `sqlc`, `oapi-codegen`, and `go-pdf/fpdf` pages, then put the options to the owner with a recommendation for each.
- **What the human decided.** Hamza chose all four: `apps/api` and `apps/dashboard` as two Vercel projects (not the Services beta), standard library routing, `pgx` with `sqlc`, and Typst for PDFs subject to a spike.
- **How it was checked.** The research changed the plan: the spike had assumed one Vercel function per Go file, but the Go runtime now runs a whole HTTP server, so the API becomes one server. Facts are linked with the date they were checked. What is not proven (Typst running on Vercel, the ZUGFeRD XMP block) is stated in ADR 0006, which stays Proposed until a spike passes.
- **Review before commit.** Hamza asked the agent to review its own drafts instead of handing him a checklist. This was a self-review, not an independent one. The agent re-checked each claim against its source and found: an unsourced statement about a PDF library's license (removed); imprecise wording about Go `embed` and Vercel build skipping (fixed); and, while reading the `pgx` source to confirm how simple protocol handles parameters, that `pgx` recommends a different mode (`QueryExecModeExec`) that also avoids named prepared statements. It tested that mode against the live pooler with read-only queries before changing ADR 0005, which now uses it with the simple protocol as fallback. The lesson: reading the library's own source during review turned up a better option than the one the spike had settled on.

### 2026-11-01: Review process and the BMF FAQ

- **Review process.** Hamza had asked about prompting the agent as a "team" of role-played specialists. The agent advised against it: one model with persona labels gives no independent review, and a fixed sequence of roles puts QA and security last. Hamza agreed to the alternative, recorded above under "How each slice is reviewed": role checklists that are applied to every slice, plus review steps that do not come from the authoring session (CI, `/code-review`, `/security-review`, and his own review).
- **BMF FAQ.** Hamza saved the BMF FAQ page as a PDF by hand, because the site blocks automated tools. The agent read all 12 pages and added only the points the letters did not already cover to `docs/domain/germany-e-invoicing.md`, cited by question number. It also checked a vendor blog's claim that the FAQ revision made PDF invoices newly invalid, and found that the FAQ says no such thing.

### 2026-11-01: EN 16931 calculation rules and the data model draft

- **What the agent did.** It closed the open gap on EN 16931 calculation rules by reading the business rules in CEN's published validation artefacts and KoSIT's XRechnung rules (both open source, files hashed), and wrote `docs/domain/germany-en16931-calculation.md`. It then drafted the data model and schema spec in `docs/design/data-model.md`, with nine decisions (D1 to D9) put to the owner, each with a recommendation.
- **What the human decided.** Hamza accepted the recommendation for all nine decisions, recorded in ADR 0007. The agent then read the NTA's full invoice Q&A to settle the open question on Japanese corrections: the NTA expects a corrected invoice that shows its link to the original, so "void and issue a new invoice" is not enough. That became a new proposal, D10 (offer corrected invoices in phase 1), which he also accepted.
- **How it was checked.** Reading the validator's test expressions, not only the rule texts, showed that the CEN rule for the VAT amount accepts a deviation of up to one whole currency unit, while German law requires the exact cent. The spec therefore requires the platform's own tests to assert exact amounts. In self-review the agent removed an unsourced example (a past German rate change), replaced a shaky legal argument for D4 with one the domain notes support, found that a `check` constraint cannot validate currency decimals on child tables (moved to the parent-reading trigger), and turned an assumption about Supabase's default grants into a test.

### 2026-11-01: API and webhook contract (ADRs 0008 and 0009)

- **What the agent did.** It read the current standards (RFC 9457 problem details, the IETF Idempotency-Key draft -08, the Standard Webhooks specification, and the `oapi-codegen` releases) and drafted `docs/design/api.md`: conventions, errors, idempotency, draft concurrency, pagination, the phase 1 endpoint list, and webhook signing, payload, and retries, with thirteen decisions (A1 to A8, W1 to W5) put to the owner, each with a recommendation.
- **What the human decided.** Hamza accepted the recommendation for all thirteen decisions, recorded in ADR 0008 (A1 to A8) and ADR 0009 (W1 to W5). The agent then wrote `openapi/openapi.yaml` and `docs/api/errors.md` and applied the resulting changes to the data model.
- **How the contract was checked.** The spec passes `redocly lint` with the recommended rules. The agent generated strict `net/http` server code from it with `oapi-codegen` in a throwaway module and compiled it. That run found two problems that reading the docs had not: schemas used only by the `webhooks` section were pruned, which broke the build, and plain pointers cannot tell an absent field from `null`, which JSON Merge Patch needs. Both are fixed by generator settings, now recorded in the design document and ADR 0008, and the second has a test. Re-reading the Idempotency-Key draft also showed that the header value is a quoted Structured Field string, so the spec accepts both the quoted and the bare form.
- **How the design was checked.** The idempotency design relies on a transaction-scoped advisory lock to answer concurrent repeats with `409`. Before writing it down, the agent tested that lock through the Supabase transaction pooler with `pgx` exec mode (a second transaction is refused while the first holds it, and gets it after commit). The claim in ADR 0005 that `oapi-codegen` supports OpenAPI 3.1 was confirmed: support arrived in v2.8.0, and the two releases after it fix code-injection issues in generation, so the spec sets v2.8.2 as the minimum. In self-review the agent found that replaying stored responses would have kept plaintext API keys and webhook secrets in `idempotency_keys` for 24 hours, and changed the design to redact them; it also corrected a sentence that attributed a 5-minute replay tolerance to the webhook spec, which sets no value.
- **Three details delegated to the agent.** Reviewing the diff, Hamza asked the agent to decide three points it had flagged (the GitHub URL in error `type`, rejecting unknown request fields, and the line between `409` and `422`), on condition that each was the best-supported option. Checking the primary texts changed two of them. RFC 9457 makes `type`, not an extension member, the identifier clients must use, so the docs that told clients to branch on `code` were wrong: `type` is now a frozen identifier and `code` its short form, and each type now has the documented title the RFC requires. RFC 9110 defines `422` by the request content, and `POST /issue` has none, so an incomplete draft or an over-large credit note is now `409`. Rejecting unknown fields was kept, with Zalando's guideline 109 as the source.

### 2026-11-02: API skeleton, CI, and the Vercel project (ADR 0010)

- **What the agent did.** It checked the machine's toolchain (Go 1.27.3, Supabase CLI, no Docker), put four scope questions to the owner, then wrote the Go skeleton in `apps/api`: code generated from the contract, a `net/http` server with request IDs, panic recovery, logging, a body limit, and problem-details responses for unmatched routes, a `pgx` pool in exec mode, and tests. It wrote the CI workflow, created and deployed the Vercel project `jade-invoicing-api`, and drafted ADR 0010 and the phase 0 interview notes.
- **What the human decided.** Hamza chose Docker Desktop for the local stack (he installs it), moved the schema migration to its own step, deferred the dashboard scaffold, and asked for the skeleton to be deployed now. The agent chose the validation library, the linters, and the CI layout and recorded them in ADR 0010 with their alternatives, for him to review.
- **How it was checked.** Both candidate validators were run against the real contract before choosing; that spike found that both report valid fields as unknown when a body has another error, which is now a rule for the phase 1 validation layer. The contract tests have a self-test that a broken response is rejected, and a test ties each problem type in Go to its entry in `docs/api/errors.md`. The deployment proved that Vercel builds Go 1.27.3 from `go.mod` and serves from `fra1`. The database smoke tests passed against the Supabase transaction pooler. `golangci-lint`, `actionlint`, and `redocly lint` report no issues. In self-review the agent removed a `.gitignore` that the Vercel CLI added inside `apps/api` (the root one already covers it), and checked in the Vercel API spec that the project's `createDeployments: disabled` setting concerns GitHub Deployment objects, not builds.
- **First CI run (2026-11-02, `0cb41bd`).** All three jobs passed on the first run. The log shows `supabase db start` applying the migration and the database tests running, not skipped; the push also deployed through the Git integration. The local stack still waits for Docker Desktop.
