package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/apikey"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/db"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/problem"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/store"
)

// unauthorizedDetail is the same for every rejected key, so the response does
// not tell an attacker whether a prefix exists, or whether a key expired or
// was revoked (docs/design/api.md, acceptance criterion 9).
const unauthorizedDetail = "Send a valid API key in the Authorization header as a Bearer token."

// dummyHash is compared against when a prefix is unknown, so an unknown key
// costs the same hash and comparison as a known one. It is random, so no
// secret matches it.
var dummyHash = func() [32]byte {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return sha256.Sum256(b[:])
}()

// tenantRequestTimeout bounds a tenant request's database work. Without it a
// stalled pooler connection or a lock wait would hold the request, and one of
// the pool's few connections, until the platform kills the function, and the
// client would get no problem response.
const tenantRequestTimeout = 10 * time.Second

// errRollback ends a tenant transaction without committing. The response in
// the buffer is still sent: it is a 4xx or 5xx the handler chose.
var errRollback = errors.New("rollback: response is an error")

type tenantKey struct{}

// tenantRequest is what an authenticated request carries: the transaction
// every query of the request runs in, with the tenant already set for RLS.
type tenantRequest struct {
	tx     pgx.Tx
	tenant store.AppTenant
	keyID  uuid.UUID // the API key making the request, the audit log's actor
}

func tenantFrom(ctx context.Context) tenantRequest {
	tr, ok := ctx.Value(tenantKey{}).(tenantRequest)
	if !ok {
		// Only routes behind authenticate call this.
		panic("httpserver: tenant handler outside authenticate")
	}
	return tr
}

// queries returns the store bound to the request's tenant transaction.
func queries(ctx context.Context) *store.Queries {
	return store.New(tenantFrom(ctx).tx)
}

// authenticate checks the API key, then runs the rest of the request in one
// transaction scoped to the key's tenant (ADR 0005, ADR 0012):
//
//  1. Parse "Authorization: Bearer jade_sk_<prefix>_<secret>". A malformed
//     value is rejected without touching the database.
//  2. Look the prefix up with app.find_api_key, which works before the tenant
//     is known, and compare SHA-256(secret) with the stored hash in constant
//     time. Unknown, wrong, expired, and revoked keys all get the same 401.
//  3. In db.InTenantTx: read the tenant (403 if suspended), record the key's
//     use at most once a minute, and call next with a buffered writer.
//  4. Commit if the response is below 400, roll back otherwise, and only then
//     send it. A failed commit becomes a 500 instead of a false success.
func authenticate(log *slog.Logger, pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), tenantRequestTimeout)
			defer cancel()
			id := RequestID(ctx)
			if pool == nil {
				log.Error("database not configured: JADE_API_DATABASE_URL is not set", "request_id", id)
				problem.Write(w, problem.New(problem.Internal, id, ""))
				return
			}

			prefix, secret, ok := bearerKey(r.Header.Get("Authorization"))
			if !ok {
				unauthorized(w, id)
				return
			}
			key, found, err := findKey(ctx, pool, prefix)
			if err != nil {
				log.Error("find api key", "request_id", id, "error", err)
				problem.Write(w, problem.New(problem.Internal, id, ""))
				return
			}
			want := dummyHash[:]
			if found {
				want = key.SecretHash
			}
			got := apikey.Hash(secret)
			match := subtle.ConstantTimeCompare(got[:], want) == 1
			if !found || !match || key.RevokedAt != nil || (key.ExpiresAt != nil && !time.Now().Before(*key.ExpiresAt)) {
				unauthorized(w, id)
				return
			}

			buf := newBufferWriter()
			err = db.InTenantTx(ctx, pool, key.TenantID, func(tx pgx.Tx) error {
				q := store.New(tx)
				tenant, err := q.GetCurrentTenant(ctx)
				if err != nil {
					return fmt.Errorf("read tenant: %w", err)
				}
				if tenant.Status != "active" {
					problem.Write(buf, problem.New(problem.Forbidden, id, "The tenant is suspended."))
					return errRollback
				}
				if err := q.TouchAPIKey(ctx, key.ID); err != nil {
					return fmt.Errorf("record key use: %w", err)
				}
				tr := tenantRequest{tx: tx, tenant: tenant, keyID: key.ID}
				next.ServeHTTP(buf, r.WithContext(context.WithValue(ctx, tenantKey{}, tr)))
				if buf.Status() >= 400 {
					return errRollback
				}
				return nil
			})
			if err != nil && !errors.Is(err, errRollback) {
				log.Error("tenant transaction", "request_id", id, "error", err)
				problem.Write(w, problem.New(problem.Internal, id, ""))
				return
			}
			buf.flushTo(w)
		})
	}
}

func findKey(ctx context.Context, pool *pgxpool.Pool, prefix string) (store.FoundAPIKey, bool, error) {
	key, err := store.New(pool).FindAPIKey(ctx, prefix)
	if errors.Is(err, pgx.ErrNoRows) {
		return key, false, nil
	}
	return key, err == nil, err
}

// bearerKey reads an API key from an Authorization header. The scheme is
// case-insensitive (RFC 9110 §11.1); the key itself must match the exact
// format.
func bearerKey(header string) (prefix, secret string, ok bool) {
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", "", false
	}
	return apikey.Parse(strings.TrimLeft(token, " "))
}

func unauthorized(w http.ResponseWriter, requestID string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	problem.Write(w, problem.New(problem.Unauthorized, requestID, unauthorizedDetail))
}
