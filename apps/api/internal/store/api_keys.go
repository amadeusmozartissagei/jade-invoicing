package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// FoundAPIKey is what authentication needs to check a presented key.
type FoundAPIKey struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	SecretHash []byte
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

const findAPIKey = `select id, tenant_id, secret_hash, expires_at, revoked_at from app.find_api_key($1)` //nolint:gosec // G101: SQL text with a column name, not a credential

// FindAPIKey looks a key up by its prefix, across tenants. Authentication runs
// before the tenant is known, so it goes through the security definer
// function instead of the RLS-protected table. It returns pgx.ErrNoRows for
// an unknown prefix; the caller compares the hash and rejects expired or
// revoked keys.
func (q *Queries) FindAPIKey(ctx context.Context, prefix string) (FoundAPIKey, error) {
	var k FoundAPIKey
	err := q.db.QueryRow(ctx, findAPIKey, prefix).Scan(&k.ID, &k.TenantID, &k.SecretHash, &k.ExpiresAt, &k.RevokedAt)
	return k, err
}
