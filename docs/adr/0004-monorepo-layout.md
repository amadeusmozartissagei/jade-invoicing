# 0004. Monorepo layout: apps/api and apps/dashboard as two Vercel projects

- **Status:** Accepted
- **Date:** 2026-11-01
- **Decided by:** Hamza Pratama (owner), with research by the AI coding agent

## Context

The repository will hold a Go API, a Next.js admin dashboard, Supabase migrations, an OpenAPI contract, Terraform, and docs. The layout has to fit how Vercel builds each part. Checked on 2026-11-01:

- The Vercel Go runtime (beta) can now run a whole Go HTTP server, not only one function per file in `api/`. The Go framework preset "detects a root `go.mod` file and one of these entrypoints: `main.go`, `cmd/api/main.go`, or `cmd/server/main.go`", the server listens on `PORT`, and `vercel.json` must set `framework` to `go`. `go.mod` must be at the project root. ([Go runtime][vercel-go], page updated 2026-08-11)
- A file named `api/*.go` at a project root still becomes a separate Vercel Function, so a Go module must not live in a folder called `api/` at the root of its Vercel project. ([Go runtime][vercel-go])
- One repository can feed several Vercel projects, each with its own Root Directory. Hobby allows 25 projects per Git repository and 100 deployments per day. ([Monorepos][vercel-monorepos], [Limits][vercel-limits], page updated 2026-09-16)
- Vercel can skip builds of unchanged projects automatically, but only for npm, yarn, pnpm, or Bun workspaces. Other changes count as global and rebuild every project. ([Monorepos][vercel-monorepos])
- Vercel Services (beta) can put a Go server and a Next.js app in one project behind one domain, routed by rewrites. Services are billed like Functions, and Hobby includes 1,000,000 service-to-service requests. ([Services][vercel-services], [Services pricing][vercel-services-pricing])

The spike (ADR 0003) used one-file-per-function Go handlers in `api/`. That was fine for two endpoints but would mean a separate function, cold start, and database pool per endpoint for a real API.

## Options

**A. `apps/api` and `apps/dashboard`, deployed as two Vercel projects.** Each app has its own Root Directory and `vercel.json`. Uses only the Go runtime beta that ADR 0001 already accepted. The dashboard and the API sit on different domains.

**B. Same folders, one Vercel project using Services.** One domain, no CORS, one place for firewall and protection settings. Adds a second beta dependency (Services) on top of the Go runtime beta.

**C. Go API at the repository root, dashboard in a subfolder.** Shortest paths for Go, but the root then mixes Go files with docs, Supabase, and Terraform, and the API looks like the whole project.

## Decision

Option A. The layout is:

```
apps/
  api/                 Go module, one HTTP server; Vercel project root
    go.mod
    cmd/api/main.go    entrypoint (Vercel Go preset), listens on PORT
    internal/          handlers, domain, tax rules, database, jobs, PDF
    sqlc.yaml
    vercel.json        framework: go, region fra1
  dashboard/           Next.js + TypeScript; Vercel project root
openapi/               OpenAPI contract, shared by both apps
supabase/              config.toml and migrations (Supabase CLI)
infra/terraform/       Vercel and Supabase resources
docs/                  ADRs, domain notes, interview notes
.github/workflows/     CI
```

The Go API, the webhook dispatcher, and the queue drain endpoint (ADR 0003) are one Go module and one server. They share the database pool and domain code, and they deploy together.

The package layout inside `apps/api/internal/` is settled when the skeleton is written, not here.

## Consequences

**Positive**

- No new beta feature. If Services leaves beta and proves useful, moving to Option B changes only `vercel.json` files, not the folder layout.
- One Go server means one cold start and one connection pool per instance, and a normal `net/http` router (ADR 0005).
- Each app builds, tests, and deploys on its own, which also suits parallel work in separate git worktrees once the contracts are fixed.
- `openapi/` and `supabase/` at the root make the contracts visible as shared, not owned by one app.

**Negative, and how we handle it**

- **Two domains.** Browser calls from the dashboard to the API need CORS, or must go through the dashboard's own server side. Decided in phase 3, when the dashboard's auth model is designed.
- **No automatic build skipping**, because the Go app is not a JavaScript workspace. Each project gets an Ignored Build Step that skips the build when nothing changed under its root or in the shared folders it uses (`openapi/` for both apps). Skipped builds of this kind still count toward the 100 deployments per day, which is far above what this project needs.
- **Go `embed` only reads files in the embedding package's own directory or below.** Anything the server needs at run time (PDF templates, fonts, a copy of the OpenAPI file) lives inside `apps/api/`, next to the package that embeds it, or is copied there during the build.
- **Slightly longer paths** (`apps/api/...`) in commands and CI.

## Follow-ups

- Create both Vercel projects (`fra1`) and set their Root Directories when the skeleton exists; later manage them with Terraform (phase 5).
- Pick the Go version for `go.mod` (the spike used 1.25; the local toolchain is 1.27) and check that Vercel builds it.
- Retire the `jade-invoicing-spike` Vercel project.

[vercel-go]: https://vercel.com/docs/functions/runtimes/go
[vercel-monorepos]: https://vercel.com/docs/monorepos
[vercel-limits]: https://vercel.com/docs/limits
[vercel-services]: https://vercel.com/docs/services
[vercel-services-pricing]: https://vercel.com/docs/services/pricing
