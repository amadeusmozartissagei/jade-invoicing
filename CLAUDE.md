# jade-invoicing: project context

`jade-invoicing` (JApan + DEutschland) is an invoicing platform for Japanese and German invoice rules.

This file carries over the decisions made before this repository existed. Read it at the start of every session. Update it when a decision changes. Detailed reasoning goes in `docs/adr/`, not here.

> This repository is public. Keep personal data (phone, ID numbers, address) and any confidential employer details out of it.

## Owner

Hamza Pratama, software engineer, Informatics graduate from Universitas Negeri Semarang (2026). Background:

- Backend work on a multi-tenant e-KYC platform at Lintasarta: partner REST APIs, asynchronous verification, callbacks, Postgres, Redis, object storage.
- Full-stack internal apps (Next.js, TypeScript, Supabase).
- Applied computer vision: liveness detection with YOLO/PyTorch and TrOCR fine-tuning.

Portfolio: https://hamza-pratama.is-a.dev

Chat with him in casual Indonesian (lo/gw). Code, commits, and docs are in English.

## Why this project exists

He is targeting software engineering jobs in **Japan and Germany**. On 2026-10-28 we sampled job boards: Japan Dev (~278 listings), TokyoDev (135), and Arbeitnow (287 German tech listings). Findings:

- Entry-level roles are scarce. Most ask for 3–5 years of experience, so the portfolio has to show production-grade engineering, not tutorials.
- Skills common to both countries: Python, AWS, Docker/Kubernetes, Postgres, CI/CD, TypeScript/React. Go is notable in Japanese fintech (Money Forward, PayPay group).
- Fintech is the largest employer on English-speaking Japan boards.
- German employers value data-protection awareness (GDPR/DSGVO).

The goal: one deep, deployed, well-documented project that he can defend in interviews. Breadth matters less than depth.

The project started as an e-KYC platform and was changed to invoicing on 2026-11-01, because the AI part of e-KYC could not run well on free hosting. See `docs/adr/0001-pivot-to-invoicing-on-free-tier.md`.

## The project

An open-source, multi-tenant **invoicing platform that follows Japanese and German invoice rules**. A client company integrates it through an API to issue compliant invoices to its customers.

1. A tenant (client company) registers and gets API keys.
2. The client manages its customers and creates invoices (draft, then issued; issued invoices are immutable and corrected only by credit notes).
3. The platform applies country rules: tax rates, rounding, required fields, and invoice numbering.
4. Output: a PDF, and for Germany a structured e-invoice (XRechnung XML, ZUGFeRD hybrid PDF).
5. Events (issued, paid, voided) go to the client by webhook (HMAC-signed, with retries and idempotency) and through status polling.
6. An admin dashboard covers audit search, tenant quotas, and export.
7. GDPR features: field-level encryption of personal data, retention policies, and data subject export and erasure. Erasure conflicts with legally required invoice retention, and resolving that is a planned design topic.

### Domain scope

Researched on 2026-11-01. Details and primary sources are in `docs/domain/`.

- **Japan:** the Qualified Invoice System (適格請求書等保存方式), in force since October 2023. Registration number `T` + 13 digits, rates 10% and 8%, tax rounded once per rate per invoice, return and corrected invoices, 7-year retention.
- **Germany:** B2B e-invoicing. Everyone must be able to receive E-Rechnungen since 2025. Issuers may still send PDF or paper until end of 2026, or end of 2027 if their prior-year turnover was at most €800,000. Formats: XRechnung (EN 16931) and ZUGFeRD 2.0.1+ except MINIMUM and BASIC-WL. The XML is the invoice. Rates 19% and 7%, sequential invoice numbers, 8-year retention.
- Do not write a regulatory claim into code or docs without a source. Known gaps are listed in `docs/domain/README.md`.

### Hard constraints

- **Fully free to run.** Every deployed component must run at $0 on free tiers. If a choice needs a paid plan or a beta feature, it needs an ADR. Free-tier limits and their sources are recorded in ADR 0001.
- Build everything from scratch. No code, vendor names, client names, endpoints, or internal details from Lintasarta.
- Synthetic data only. No real companies, real registration or tax numbers, or real people in the repository, tests, or demos.
- Open-source dependencies only. Record the license of anything non-trivial.

## Stack decisions (agreed)

| Part | Choice | Reason |
|---|---|---|
| Public REST API, webhook dispatcher | **Go** on Vercel (Go runtime, beta) | Common in Japanese fintech; free on Vercel Hobby |
| Admin dashboard | **Next.js + TypeScript** on Vercel | His existing strength |
| Database, auth, file storage | **Supabase** (Postgres, Auth, Storage) | Free tier; real Postgres |
| Background jobs and retries | Supabase `pgmq` + `pg_cron` + `pg_net` | Vercel Hobby cron runs only once a day |
| Infrastructure as code | Terraform (Vercel and Supabase providers) | IaC signal without AWS cost |
| CI/CD | GitHub Actions | Free for public repositories |
| Local environment | Docker (Supabase CLI local stack) | |

Free-tier limits that shape the design (details and sources in ADR 0001):

- Vercel function request and response bodies are capped at 4.5 MB. Large files go directly to Supabase Storage through signed URLs.
- Vercel Hobby is for non-commercial use only. That is fine for an open-source portfolio.
- A Supabase Free project pauses after a week without activity. The live demo needs a keep-alive job.

Still open, to settle in the first sessions with an ADR each:

- How to generate and validate XRechnung and ZUGFeRD. The official validator is Java, so it may run in CI only.
- PDF engine: Typst is Proposed (ADR 0006), pending a spike on Vercel early in phase 1; fallback is a pure-Go library. The ZUGFeRD XMP block is unproven.

Settled:

- The repository name is `jade-invoicing` (ADR 0002).
- Layout: `apps/api` (one Go module, one HTTP server via the Vercel Go preset, `cmd/api/main.go`) and `apps/dashboard` (Next.js) as two Vercel projects; `openapi/`, `supabase/`, `infra/terraform/` at the root (ADR 0004).
- Go: standard library `net/http` routing; `pgx` v5 in `QueryExecModeExec` (tested through the transaction pooler; simple protocol is the fallback) with `sqlc`; tenant set per transaction with `set_config(..., true)` for RLS (ADR 0005). ADR template: `docs/adr/template.md`.
- Background jobs: `pgmq` queues, drained by a Go endpoint on Vercel that `pg_cron` + `pg_net` call every few seconds (ADR 0003).
- Data model: `docs/design/data-model.md` (ADR 0007). API contract: `openapi/openapi.yaml` (OpenAPI 3.1, hand-written) with Go generated by `oapi-codegen` ≥ v2.8.2 in strict `std-http-server` mode; RFC 9457 errors, `Idempotency-Key` on every `POST`, `If-Match` on drafts, keyset pagination, decimal strings for money (ADR 0008). Webhooks: Standard Webhooks signing, payloads without personal data, 10 attempts over ~3 days (ADR 0009). Design: `docs/design/api.md`; error codes: `docs/api/errors.md`. Supabase project: Free plan, Frankfurt, Data API off, RLS on by default. Database changes go through `supabase/migrations/`, not the dashboard.
- Skeleton and CI (ADR 0010): Go 1.27.3; `libopenapi-validator` checks responses against the contract in tests and will drive request validation; `golangci-lint` v2; CI runs OpenAPI lint, Go lint/build/codegen-drift, and tests with `-race` against `supabase db start`; actions pinned to SHAs. Vercel project `jade-invoicing-api` (Root Directory `apps/api`, fra1, deploys from `main`). Dev commands: `apps/api/README.md`.
- Schema (ADR 0011): migrations `supabase/migrations/20261102120*` implement `docs/design/data-model.md`, with one test per acceptance criterion in `apps/api/internal/store/schema_test.go`. API role `jade_api` is created without a password (set by hand per environment). Money is plain `numeric` checked by `app.fits_currency` (rejects extra decimals instead of rounding); Go uses `shopspring/decimal` v1.6.0. `sqlc` v1.31.1 generates `apps/api/internal/store`. Every migration that adds functions in `app` must revoke `EXECUTE` from `PUBLIC`.
- Auth and validation (ADR 0012): keys `jade_sk_<prefix>_<secret>` in lowercase base32, SHA-256 of the secret stored, created by `apps/api/cmd/admin create-tenant` (as `postgres`, prints the key once). Every `/v1` request runs as: authenticate (identical `401`s, `403` if suspended) → one `db.InTenantTx` → validate against the embedded copy of `openapi.yaml` → handler. Responses are buffered: commit, then send; `4xx`/`5xx` roll back. Only implemented operations are routed. Server env var `JADE_API_DATABASE_URL`; without it `/healthz` works and `/v1` answers `500`.
- Keep-alive (ADR 0013): `GET /readyz` runs `select 1` as `jade_api` (`200`/`503`); `.github/workflows/keep-alive.yml` calls it at 05:17 and 17:17 UTC so the Supabase Free project does not pause. `/healthz` never touches the database.

## Roadmap (draft; refine in phase 0)

Each phase ends deployed or runnable, with tests, ADRs, and interview notes.

- **Phase 0: Foundation.** Domain research with citations, the free-tier spike, repository layout, local stack, CI on GitHub Actions (lint, test, build), OpenAPI contract, first database schema, ADR template.
- **Phase 1: Core flow MVP.** Tenants and API keys, customers, invoice lifecycle (draft, issue, void, credit note), per-tenant numbering, Japanese and German tax calculation, PDF output, status polling, and webhook delivery with HMAC signing, retries, and idempotency. A first public demo deployment.
- **Phase 2: E-invoice compliance.** XRechnung and ZUGFeRD export, Japanese qualified-invoice checks, and a conformance test suite against official examples.
- **Phase 3: Dashboard.** Admin UI, audit log, quotas, export.
- **Phase 4: Security and GDPR.** Field-level encryption, key rotation, retention versus erasure, data subject export, rate limiting, threat-model notes.
- **Phase 5: Operations.** Terraform for every environment, OpenTelemetry tracing on a free backend, metrics, load testing within free quotas, and alerts on quota use.
- **Phase 6: Presentation.** Architecture diagram, demo video, a bilingual README (English plus a Japanese summary), and a case study on his portfolio site.

He can start applying after phase 1 or 2. Later phases keep deepening the project.

## Working agreement: agentic AI workflow

Claude writes the code. Hamza steers, reviews, and must be able to explain every part in an interview. He will say openly that he built it with an AI coding agent, so the workflow itself is part of what the project demonstrates.

Loop for each feature or slice:

1. **Spec.** Agree on the slice's goal and its acceptance criteria.
2. **Plan.** Use plan mode for anything non-trivial and record design choices.
3. **Implement in small slices.** Each slice comes with its own tests.
4. **Verify.** Run the tests, linters, and the local stack, and show proof (test output, request/response, screenshots).
5. **Review.** Run a code review pass (`/code-review`) before a commit or PR.
6. **Explain.** Write or update the ADR and the interview notes for the slice.
7. **Commit or PR** through CI.

Parallel work: once the contracts are fixed (OpenAPI spec, database schema, webhook payloads), the Go API and the dashboard can run as separate sessions in separate git worktrees. Never let two sessions edit the same files.

Documentation that must stay current:

- `docs/adr/NNNN-title.md`: each significant decision, with context, options, decision, and consequences.
- `docs/domain/`: Japanese and German invoice rules, each with its primary source.
- `docs/interview-notes/phase-N.md`: likely interview questions on that phase and clear answers in plain language.
- `docs/ai-workflow.md`: how the AI agent was used, what humans decided, and how quality was checked (tests, CI, reviews). This is honest material for the "you used AI?" interview question.

Quiz Hamza on the design (why this, what fails if X goes down, how it scales) and revise the notes wherever his explanation is shaky. Decided 2026-11-02: the quizzes are held back until development is done, then cover every phase; do not offer one at the end of each phase. Keep writing each phase's interview notes as it goes.

He still practices live coding (LeetCode easy/medium in Go or Python) on his own. Live interview rounds are usually without AI.

## Conventions

- Commit messages: English, technical, conventional style (`feat:`, `fix:`, `chore:`…). **Never add a `Co-Authored-By: Claude` trailer** (owner's standing rule).
- Commit or push only when he asks.
- Prefer boring, well-known tools over novelty unless an ADR justifies the choice.
