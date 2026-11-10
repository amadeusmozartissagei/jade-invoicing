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

