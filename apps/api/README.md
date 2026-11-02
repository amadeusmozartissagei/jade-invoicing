# apps/api

The jade-invoicing HTTP API: one Go server (ADR 0004), deployed to Vercel as the project `jade-invoicing-api`. The contract is [`openapi/openapi.yaml`](../../openapi/openapi.yaml); tooling choices are in ADR 0010.

## Requirements

- Go 1.27.3 or newer (the `go` line in `go.mod`)
- [Supabase CLI](https://supabase.com/docs/guides/local-development/cli/getting-started) and Docker Desktop, for the database tests
- `golangci-lint` v2.15.0, for linting

## Commands

Run from `apps/api` unless noted.

```bash
go run ./cmd/api                      # serves on :8080 (PORT overrides)
go test ./...                         # database tests skip without TEST_DATABASE_URL
go generate ./...                     # regenerate internal/api from openapi.yaml
golangci-lint run ./...
```

Database tests against the local stack (from the repository root):

```bash
supabase db start
```

```bash
TEST_DATABASE_URL=postgresql://postgres:postgres@127.0.0.1:54322/postgres go test ./...
```

## Layout

| Path | What |
|---|---|
| `cmd/api` | Entrypoint; Vercel's Go preset starts it and sets `PORT` |
| `internal/api` | Generated from the contract by `oapi-codegen`; do not edit `api.gen.go` |
| `internal/httpserver` | Routes and middleware |
| `internal/problem` | RFC 9457 error responses (codes in `docs/api/errors.md`) |
| `internal/db` | Postgres pool (`pgx`, exec mode) |
| `internal/contracttest` | Test helper that checks responses against the contract |
