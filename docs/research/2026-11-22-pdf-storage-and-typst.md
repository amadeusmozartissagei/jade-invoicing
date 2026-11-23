# Research: PDF storage on Supabase and Typst on Vercel

- **Date checked:** 2026-11-22
- **For:** the invoice PDF slice (render with Typst in a background job, store in a private Supabase Storage bucket, hand out 5-minute signed URLs).
- **Already recorded elsewhere:** free-tier limits (ADR 0001), the `pgmq` + `pg_cron` worker (ADR 0003), Typst as the proposed engine and its spike criteria (ADR 0006). This note adds the facts those ADRs left open.
- **Method:** primary sources only (official docs, the projects' GitHub repositories, release pages, source code at a pinned tag). Each claim carries a source key; keys are listed at the end. "Unconfirmed" means no primary source said it.

## A. Supabase Storage from a server

### A1. Credentials that can upload to a private bucket

| Credential | What it grants |
|---|---|
| Secret key / legacy `service_role` key | Authorizes as the built-in `service_role` Postgres role, which has `BYPASSRLS`; "full access to your project's data" [sb-api-keys]. For Storage, service keys "entirely bypass RLS policies, granting you unrestricted access to all Storage APIs" [sb-storage-ac]. It is a project-wide key, not a Storage-only key. |
| S3 protocol access key (Storage settings, S3 connection) | "Full access to all S3 operations across all buckets and bypass RLS policies"; server-side only [sb-s3-auth]. In the Storage server, a dashboard-style access key is stored with a claims object (`role`, `sub`) and requests run with those claims [storage-sigv4-plugin, storage-admin-s3]. The docs say nothing about database access through this key, and the Storage server only uses it to sign requests to its own S3 routes [storage-sigv4-plugin]. So it is scoped to Storage, but within Storage it is all-powerful. Which role the dashboard assigns, and whether a custom role can be chosen: **Unconfirmed** (the admin endpoint accepts any `role` claim [storage-admin-s3]; the dashboard flow is not documented). |
| JWT carrying a custom role | Storage sets `set_config('role', <JWT role claim>, true)` per request and evaluates RLS on `storage.objects` as that role [storage-scope]. Uploading needs only an `INSERT` policy on `storage.objects`; upsert also needs `SELECT` and `UPDATE` [sb-storage-ac]. The server would mint the JWT itself, which needs a project signing key: Supabase lets you import a private key or set a shared secret and mint JWTs with `role` and `exp` [sb-signing-keys]; it recommends ES256 over HS256 [sb-signing-keys]. The same JWT also works as an S3 session token (access key id = project ref, secret = anon key, session token = JWT; publishable keys "not yet supported") [sb-s3-auth]. **Unconfirmed:** whether a custom role must be granted to the Storage service's database user for `set role` to succeed (Postgres requires membership; no Supabase doc covers custom roles in Storage). Note: a key that can mint JWTs can mint any role, including `service_role`, so it is not a smaller secret than the service key. |

### A2. Presigned GET URLs

- **S3 presigning works.** "Storage supports presigning a URL using query parameters", with AWS Signature Version 4 [sb-s3-compat]. Standard SigV4 query presigning (for example `aws-sdk-go-v2`'s `s3.PresignClient`) produces exactly that form; the Storage server reads `X-Amz-Expires`, `X-Amz-Security-Token` and friends from the query string [storage-sigv4].
- **Max expiry for S3 presigned URLs: 604,800 s (7 days).** `MAX_PRESIGNED_EXPIRY_SECONDS = 604800`; larger values fail with "Invalid expiration" [storage-sigv4]. 300 s is well inside.
- **REST "create signed URL"**: `POST /storage/v1/object/sign/{bucket}/{path}` with body `{"expiresIn": <seconds>}`, returning `signedURL` [storage-signurl]. `expiresIn` is in seconds [sb-js-signedurl]. The server first looks the object up under the caller's role (`findObject`), so the caller needs `SELECT` on that row in `storage.objects`; the service key passes because it bypasses RLS [storage-object, sb-storage-ac]. There is no product-level max beyond the JWT numeric-date check [storage-auth].
- **Revocation:** individual signed URLs cannot be revoked; delete, move, or rename the object, or rotate and revoke the URL signing key to kill all of them [sb-downloads, sb-url-signing-keys]. Rotating Auth JWT keys does not affect them [sb-url-signing-keys].

### A3. S3 protocol on Free and locally

- **Hosted:** enable it under Storage settings ("S3 connection via S3 protocol") [sb-s3-compat]. Endpoint `https://<project_ref>.storage.supabase.co/storage/v1/s3`, region = the project region, `forcePathStyle: true` in the official client example [sb-s3-auth]. For this project (Frankfurt, ADR 0003) that region is `eu-central-1`.
- **Free plan availability: Unconfirmed.** Neither the S3 docs nor the pricing page say which plans include it [sb-s3-compat, sb-pricing]. Check the Storage settings page of the Free project.
- **Local CLI:** the `supabase init` template has `[storage.s3_protocol] enabled = true` [cli-config-template]. Endpoint `http://127.0.0.1:54321/storage/v1/s3`, region `local` [sb-s3-auth]. `supabase status` prints `S3_PROTOCOL_ACCESS_KEY_ID`, `S3_PROTOCOL_ACCESS_KEY_SECRET`, `S3_PROTOCOL_REGION` and `STORAGE_S3_URL` [cli-status] (Supabase CLI v3.2.0, released 2026-11-16 [cli-release]).

### A4. Creating the private bucket reproducibly

- **SQL migration (works for `supabase db push`):** buckets "are rows in the `storage.buckets` table rather than schema objects", the diff tool never tracks DML, so "you must add data changes to the migration by hand" [sb-cli-workflows]. The documented SQL form is `insert into storage.buckets (id, name, public) values (...)` [sb-creating-buckets]. The table also has `file_size_limit` (bigint, bytes) and `allowed_mime_types` (`text[]`) [storage-mig-0013, storage-mig-0014]. Buckets are private by default (`public` defaults to false) [sb-creating-buckets].
- **Caveat:** the Storage schema guide asks you to treat storage tables as read-only and to go through the API [sb-storage-schema]. The two docs pull in different directions; a plain `insert ... on conflict do nothing` of one bucket row is what the CLI guide describes.
- **`config.toml [storage.buckets.<name>]`:** keys `public`, `file_size_limit`, `allowed_mime_types`, `objects_path` [sb-cli-config]. The template comment says "configure local storage buckets" [cli-config-template]. `supabase seed buckets --remote` pushes them to a linked project [sb-cli-ref]; `db push` is not documented to create buckets (its prompt lists migrations, roles, seed files and vault secrets) [sb-cli-ref].
- **Recommendation for this repo:** the bucket row goes in a migration, so `db push` and `supabase db reset` give the same result everywhere.

### A5. Free-plan limits

- Storage 1 GB, egress 5 GB, max file upload 50 MB, database 500 MB, pause after 1 week inactive [sb-pricing]. "For Free projects, the limit can't exceed 50 MB"; a per-bucket limit cannot exceed the global limit [sb-file-limits]. Confirmed, unchanged from ADR 0001.

## B. Typst on Vercel Go

### B1. Typst release

- Latest stable: **v0.15.1**, published 2026-07-17 (v0.15.0 on 2026-06-15) [typst-release]. License Apache-2.0 [typst-repo].
- Linux x86_64 asset: **`typst-x86_64-unknown-linux-musl.tar.xz`**, 17,462,992 bytes (about 16.7 MiB) [typst-release]. Built by the release workflow for target `x86_64-unknown-linux-musl` [typst-release-yml]; Rust links that target statically by default [rust-linkage], so the binary should need no libc on the host.
- Unpacked size: **Unconfirmed** (not published; measure in the spike).
- Checksums: **no checksum file or signature** is attached to the release and the workflow does not produce one [typst-release, typst-release-yml]. GitHub's API shows a per-asset SHA-256 digest; for this asset `sha256:a6d077d0a95eed5a2eba715b2dae06be954f624ccbf85758a03f389ded33118c` [typst-release]. Record that value as the pin and verify it on download.

### B2. Shipping the binary and data files in a Vercel Go function

- The Go preset detects `go.mod` plus `main.go`, `cmd/api/main.go` or `cmd/server/main.go`, needs `"framework": "go"`, and the server listens on `PORT`. The Go runtime is Beta on all plans [vercel-go].
- **`includeFiles`:** Vercel's general guidance is that for frameworks other than Next.js, files read at runtime are added with `includeFiles` in `vercel.json` [vercel-runtimes, vercel-json]. The Go builder's standalone-server path reads `config.includeFiles` (string or array of globs, resolved against the project root) and adds the matches to the function [vercel-go-builder]. **Unconfirmed:** which `functions` key pattern routes that config to the Go preset's single function (by analogy with Python, probably the entrypoint `cmd/api/main.go` [vercel-duration]), and whether included files keep the executable bit.
- **`go:embed`:** the Go `embed` package can embed any file, including a binary, into the Go executable [go-embed]; the server would write it to `/tmp`, `chmod 0755`, and run it. Vercel does not document this pattern.
- **`/tmp`:** read-only filesystem with "writable `/tmp` scratch space up to 500 MB", per instance, not persistent [vercel-runtimes].
- **Subprocesses:** Vercel documents running bundled binaries only indirectly (Chromium via `@sparticuz/chromium` in Node.js) [vercel-limits]. Whether `/tmp` is mounted `noexec` and whether `os/exec` is allowed in the Go runtime: **Unconfirmed** (this is ADR 0006 spike criterion 1).
- Size budget: 250 MB uncompressed per function; large functions (5 GB, beta) are Node.js, Bun and Python only, not Go [vercel-limits]. The Typst archive (~17 MB) plus fonts (~10 MB, B6) fits.
- Other limits: Hobby 2 GB / 1 vCPU, 300 s max duration, 1,024 file descriptors shared, 4.5 MB response body [vercel-limits].

### B3. Typst CLI flags (v0.15.1 source)

- **Data in:** `--input key=value` adds a string to `sys.inputs` [typst-args]; values are always strings [typst-sys]. `json()` takes "A path to a JSON file or raw JSON bytes" [typst-json] and `bytes(str)` encodes UTF-8 [typst-bytes], so `json(bytes(sys.inputs.data))` works. Simpler and without command-line length limits: write `data.json` next to the template and call `json("data.json")`. Paths resolve from the calling file; files outside the project root (default: the main file's folder, or `--root`) are blocked [typst-path].
- **Fonts:** `--font-path <DIR>` (repeatable, or `TYPST_FONT_PATHS`), `--ignore-system-fonts` (or `TYPST_IGNORE_SYSTEM_FONTS`); builds with embedded fonts also accept `--ignore-embedded-fonts` [typst-args].
- **PDF standard:** `--pdf-standard a-3b` (comma-separated list allowed; values include `a-2b`, `a-3b`, `a-3u`, `a-3a`, `a-4`, `a-4f`, `ua-1`) [typst-args, typst-pdf].
- **Attachments:** `pdf.attach(path, data?, relationship:, mime-type:, description:)`. `relationship` is one of `"source"`, `"data"`, `"alternative"`, `"supplement"` and is only used for PDF/A-3; attachments are not supported with PDF/A-2 [typst-attach]. The docs no longer mention the old name `pdf.embed`.
- **Reproducibility:** `--creation-timestamp <unix>` sets the document date [typst-args]; `set document(date: none)` omits it [typst-document].
- **stdin/stdout:** input `-` reads the main file from stdin; output `-` writes to stdout (an explicit output is required when input is `-`) [typst-args].

### B4. Custom XMP metadata

- **None in v0.15.1.** `document` exposes only title, author, description, keywords and date, which Typst writes to the info dictionary and XMP [typst-document]. The `pdf` module has `artifact`, `attach`, and three accessibility helpers, nothing for metadata [typst-pdf].
- Issue #5667 "Custom XMP metadata in PDF" (open since 2025-01-07, labels `feature request`, `pdf`) proposes a `pdf.metadata(schema: ...)` API and names ZUGFeRD / Factur-X as the use case [typst-5667]. Phase 2 still needs a post-processing step or another tool until that lands (as ADR 0006 expects).

### B6. Fonts

- **Noto Sans JP:** static OTF per weight in `notofonts/noto-cjk`, `Sans/SubsetOTF/JP/` (the Japanese subset): `NotoSansJP-Regular.otf` 4,533,028 bytes, `NotoSansJP-Bold.otf` 4,656,448 bytes [noto-cjk-jp]. Latest Sans release tag `Sans2.004` (2022-01-27) [noto-cjk-releases]. License SIL OFL 1.1 [noto-cjk-license]. Google Fonts ships only the variable `NotoSansJP[wght].ttf` (9,589,900 bytes) [gf-notosansjp].
- **Noto Sans (Latin):** static files in `notofonts/notofonts.github.io`, `fonts/NotoSans/`: hinted TTF Regular 621,572 bytes, Bold 631,484 bytes; unhinted OTF Regular 331,784, Bold 342,800 [noto-sans-ttf, noto-sans-otf]. Latest release `NotoSans-v2.015` (2024-11-20) [noto-lgc-release]. License SIL OFL 1.1 [gf-notosans-ofl]. Google Fonts ships only variable files (`NotoSans[wdth,wght].ttf`, 2,049,096 bytes) [gf-notosans].
- Total for four static files: about 10.1 MB (OTF) or 10.4 MB (hinted TTF for Latin).

## Unconfirmed points

1. Whether the S3 protocol is enabled for Free-plan projects (A3).
2. Which role the dashboard-created S3 access key carries, and whether a custom role can be chosen (A1).
3. Whether a custom JWT role must be granted to the Storage service's database user (A1).
4. Which `vercel.json` `functions` key applies `includeFiles` to the Go preset's function, and whether the executable bit survives (B2).
5. Whether `os/exec` of a binary in `/tmp` (or in the bundle) works on Vercel's Go runtime (B2; ADR 0006 spike).
6. Unpacked size of the Typst binary (B1).

## Sources

All checked 2026-11-22.

| Key | URL |
|---|---|
| sb-api-keys | https://supabase.com/docs/guides/api/api-keys |
| sb-storage-ac | https://supabase.com/docs/guides/storage/security/access-control |
| sb-s3-auth | https://supabase.com/docs/guides/storage/s3/authentication |
| sb-s3-compat | https://supabase.com/docs/guides/storage/s3/compatibility |
| sb-signing-keys | https://supabase.com/docs/guides/auth/signing-keys |
| sb-downloads | https://supabase.com/docs/guides/storage/serving/downloads |
| sb-url-signing-keys | https://supabase.com/docs/guides/storage/security/signing-keys |
| sb-js-signedurl | https://supabase.com/docs/reference/javascript/storage-from-createsignedurl |
| sb-creating-buckets | https://supabase.com/docs/guides/storage/buckets/creating-buckets |
| sb-cli-workflows | https://supabase.com/docs/guides/local-development/cli-workflows |
| sb-storage-schema | https://supabase.com/docs/guides/storage/schema/design |
| sb-cli-config | https://supabase.com/docs/guides/local-development/cli/config |
| sb-cli-ref | https://supabase.com/docs/reference/cli/supabase-seed-buckets |
| sb-file-limits | https://supabase.com/docs/guides/storage/uploads/file-limits |
| sb-pricing | https://supabase.com/pricing |
| storage-sigv4 | https://github.com/supabase/storage/blob/v1.98.5/src/storage/protocols/s3/signature-v4.ts |
| storage-sigv4-plugin | https://github.com/supabase/storage/blob/v1.98.5/src/http/plugins/signature-v4.ts |
| storage-admin-s3 | https://github.com/supabase/storage/blob/v1.98.5/src/http/routes/admin/s3.ts |
| storage-scope | https://github.com/supabase/storage/blob/v1.98.5/src/internal/database/postgres/scope.ts |
| storage-signurl | https://github.com/supabase/storage/blob/v1.98.5/src/http/routes/object/getSignedURL.ts |
| storage-object | https://github.com/supabase/storage/blob/v1.98.5/src/storage/object.ts |
| storage-auth | https://github.com/supabase/storage/tree/v1.98.5/src/internal/auth (`assertValidNumericJWTExpiration`) |
| storage-mig-0013 | https://github.com/supabase/storage/blob/v1.98.5/migrations/tenant/0013-add-bucket-custom-limits.sql |
| storage-mig-0014 | https://github.com/supabase/storage/blob/v1.98.5/migrations/tenant/0014-use-bytes-for-max-size.sql |
| cli-config-template | https://github.com/supabase/cli/blob/develop/apps/cli/src/shared/init/testdata/templates/config.toml |
| cli-status | https://github.com/supabase/cli/blob/develop/apps/cli/src/command-internal/status-values.ts |
| cli-release | https://github.com/supabase/cli/releases/tag/v3.2.0 |
| vercel-go | https://vercel.com/docs/functions/runtimes/go |
| vercel-runtimes | https://vercel.com/docs/functions/runtimes |
| vercel-limits | https://vercel.com/docs/functions/limitations |
| vercel-json | https://vercel.com/docs/project-configuration/vercel-json |
| vercel-duration | https://vercel.com/docs/functions/configuring-functions/duration |
| vercel-go-builder | https://github.com/vercel/vercel/blob/84878e532142f0ac584953695658b324eb9e5b80/packages/go/src/standalone-server.ts |
| go-embed | https://pkg.go.dev/embed |
| typst-release | https://github.com/typst/typst/releases/tag/v0.15.1 |
| typst-release-yml | https://github.com/typst/typst/blob/v0.15.1/.github/workflows/release.yml |
| typst-repo | https://github.com/typst/typst |
| typst-args | https://github.com/typst/typst/blob/v0.15.1/crates/typst-cli/src/args.rs |
| typst-sys | https://typst.app/docs/reference/foundations/sys/ |
| typst-json | https://typst.app/docs/reference/data-loading/json/ |
| typst-bytes | https://typst.app/docs/reference/foundations/bytes/ |
| typst-path | https://typst.app/docs/reference/foundations/path/ |
| typst-pdf | https://typst.app/docs/reference/pdf/ |
| typst-attach | https://typst.app/docs/reference/pdf/attach/ |
| typst-document | https://typst.app/docs/reference/model/document/ |
| typst-5667 | https://github.com/typst/typst/issues/5667 |
| rust-linkage | https://doc.rust-lang.org/reference/linkage.html |
| noto-cjk-jp | https://github.com/notofonts/noto-cjk/tree/main/Sans/SubsetOTF/JP |
| noto-cjk-releases | https://github.com/notofonts/noto-cjk/releases |
| noto-cjk-license | https://github.com/notofonts/noto-cjk/blob/main/Sans/LICENSE |
| noto-sans-ttf | https://github.com/notofonts/notofonts.github.io/tree/main/fonts/NotoSans/hinted/ttf |
| noto-sans-otf | https://github.com/notofonts/notofonts.github.io/tree/main/fonts/NotoSans/unhinted/otf |
| noto-lgc-release | https://github.com/notofonts/latin-greek-cyrillic/releases/tag/NotoSans-v2.015 |
| gf-notosansjp | https://github.com/google/fonts/tree/main/ofl/notosansjp |
| gf-notosans | https://github.com/google/fonts/tree/main/ofl/notosans |
| gf-notosans-ofl | https://github.com/google/fonts/blob/main/ofl/notosans/OFL.txt |
