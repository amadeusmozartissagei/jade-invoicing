# 0001. Pivot from e-KYC to a Japan/Germany invoicing platform on a free-tier stack

- **Status:** Accepted
- **Date:** 2026-11-01
- **Decided by:** Hamza Pratama (owner), with research by the AI coding agent

## Context

The project is a portfolio piece for software engineering jobs in Japan and Germany. It has to show production-grade engineering that the owner can defend in interviews, and it has to stay deployed so reviewers can try it.

The first plan was an identity verification (e-KYC) platform: a Go API, a Python AI worker for ID card OCR, liveness, and face match, and later AWS with Kubernetes. The owner then added a constraint: **the deployed system must run at $0**, preferably on Vercel and Supabase.

We checked the free tiers on 2026-10-30. The limits that matter:

| Platform | Limit | Source |
|---|---|---|
| Vercel Hobby | Non-commercial, personal use only | [Hobby plan][vercel-hobby] |
| Vercel Hobby | Functions: 2 GB memory, 1 vCPU, 300 s max duration | [Functions limits][vercel-fn] |
| Vercel | Function bundle: 250 MB uncompressed, 500 MB for Python; "large functions" up to 5 GB in beta | [Functions limits][vercel-fn] |
| Vercel | Request and response body: 4.5 MB | [Functions limits][vercel-fn] |
| Vercel Hobby | Monthly: 4 Active CPU-hours, 360 GB-hours provisioned memory, 1M invocations, 100 GB fast data transfer | [Hobby plan][vercel-hobby] |
| Vercel Hobby | Cron jobs at most once per day, ±59 min precision | [Cron usage][vercel-cron] |
| Vercel Hobby | Exceeding a limit blocks the feature for up to 30 days | [Hobby plan][vercel-hobby] |
| Vercel | Go runtime is in beta | [Go runtime][vercel-go] |
| Vercel Daemons (beta) | Hobby: 1 or 2 GiB instances, 5 Active CPU-hours and 420 GB-hours memory per month; a 2 GiB instance running nonstop uses the memory quota in about 9 days | [Daemons limits][vercel-daemons] |
| Supabase Free | 500 MB database, 1 GB storage, 50 MB max upload, 5 GB egress, 50,000 MAU, 2 active projects | [Pricing][supabase-pricing] |
| Supabase Free | Projects pause after 1 week of inactivity | [Pricing][supabase-pricing] |
| Supabase | Queues (`pgmq`) and Cron (`pg_cron`) exist; the pricing page does not state whether Free includes them | [Queues][supabase-queues], [Cron][supabase-cron] |
| Hugging Face | Creating a Gradio or Docker Space requires a paid plan; free accounts may host up to 2 ZeroGPU Gradio Spaces with a 5-minute daily GPU quota | [Spaces overview][hf-spaces], [ZeroGPU][hf-zerogpu] |

For e-KYC, the effect is that the AI worker, the most interesting part, would be the most compromised part. It would need small CPU-only models within 2 GB and 1 vCPU, it would load those models again on every cold start, and it would have a tight CPU budget. Hugging Face Spaces, the first idea for hosting the worker, is no longer free for Docker Spaces.

The owner's e-KYC experience is also already on his CV, so a portfolio project in the same domain adds less new evidence.

## Options

**A. e-KYC on AWS, as first planned.** It gives the strongest infrastructure signal (AWS, Kubernetes). It breaks the $0 constraint, and a demo that is torn down to save money cannot be shown on request.

**B. e-KYC on the free tier.** A Python Vercel function with ONNX models, `pgmq` as the queue, and `pg_cron` for scheduling. It is feasible, but model choice is limited, cold starts are slow, and the AI part becomes a list of compromises to explain in interviews.

**C. Multi-tenant invoicing platform for Japanese and German invoice rules, on the free tier.** No ML, small data, and the depth sits in domain rules and backend engineering: tax calculation and rounding, immutable issued invoices, e-invoice formats (XRechnung, ZUGFeRD), webhooks, and GDPR versus legal retention. Both target countries changed their invoice rules recently (Japan in 2023; Germany from 2025), so the domain shows research into the target markets.

## Decision

Option C. Build a multi-tenant invoicing platform that follows Japanese and German invoice rules, with Go and Next.js on Vercel Hobby and Postgres, Auth, Storage, queues, and cron on Supabase Free. Terraform and GitHub Actions cover infrastructure and CI.

## Consequences

**Positive**

- Runs at $0 and can stay online for reviewers.
- Fits the free limits well: invoices are small text records, and no models need to be loaded.
- Stays close to fintech, the largest employer group on English-language Japan job boards, and brings GDPR in naturally for Germany.
- No overlap with the owner's previous employer's domain.

**Negative, and how we handle it**

- **Fewer signals for Python, AWS, and Kubernetes.** Terraform with the Vercel and Supabase providers and Docker for local development and CI keep some infrastructure signal. A later ADR can add an AWS or Kubernetes path that is never left running.
- **The computer vision showcase is gone.** The owner can still discuss that experience from his previous work.
- **It depends on beta features** (the Vercel Go runtime). If a beta changes, an ADR records the replacement.
- **Vercel Hobby cron runs only once a day.** Scheduled work (webhook retries, retention jobs) runs in Supabase with `pg_cron`.
- **The 4.5 MB body limit.** PDFs and other files go through Supabase Storage signed URLs, not through API responses.
- **Supabase pauses inactive projects.** A scheduled GitHub Actions job keeps the demo awake.
- **Domain risk.** Regulatory details must be checked against primary sources, and generating valid XRechnung or ZUGFeRD output is non-trivial (the official validator is Java).

## Follow-ups

- Spike: confirm `pgmq`, `pg_cron`, and `pg_net` on Supabase Free, and Go on Vercel connecting through the Supabase pooler.
- Write `docs/domain/` notes on Japanese and German invoice rules with primary sources.
- Recheck the free-tier limits above before the first public deployment. They change often.

[vercel-hobby]: https://vercel.com/docs/plans/hobby
[vercel-fn]: https://vercel.com/docs/functions/limitations
[vercel-cron]: https://vercel.com/docs/cron-jobs/usage-and-pricing
[vercel-go]: https://vercel.com/docs/functions/runtimes/go
[vercel-daemons]: https://vercel.com/docs/daemons/limits
[supabase-pricing]: https://supabase.com/pricing
[supabase-queues]: https://supabase.com/docs/guides/queues
[supabase-cron]: https://supabase.com/docs/guides/cron
[hf-spaces]: https://huggingface.co/docs/hub/spaces-overview
[hf-zerogpu]: https://huggingface.co/docs/hub/spaces-zerogpu
