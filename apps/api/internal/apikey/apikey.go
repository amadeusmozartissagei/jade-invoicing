// Package apikey creates and parses API keys in the format
// jade_sk_<prefix>_<secret> (docs/design/data-model.md, "api_keys"; ADR 0012).
//
// The prefix is public and used to look the key up; only the SHA-256 of the
// secret is stored. The secret is 256 random bits, not a password, so a fast
// hash is enough: nobody can guess it by brute force.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"regexp"
	"strings"
)

const (
	// Scheme starts every key. "sk" marks a secret key, so a key pasted
	// somewhere public is easy to recognise and search for.
	Scheme = "jade_sk_"

	prefixBytes = 10 // 80 bits: unique across all tenants, and public
	secretBytes = 32 // 256 bits
)

// Base32 without padding, lowercased: no '_' or '-' (base64url has both), so
// the parts split unambiguously and a double-click selects the whole key.
var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// format is the only shape Parse accepts: 16 and 52 characters are the
// unpadded base32 lengths of 10 and 32 bytes.
var format = regexp.MustCompile(`^jade_sk_([a-z2-7]{16})_([a-z2-7]{52})$`)

// Key is a newly generated key. Full is shown to the user once and never
// stored.
type Key struct {
	Prefix string
	Secret string
	Full   string
}

// Generate returns a new random key and the hash to store for it.
func Generate() (Key, [32]byte) {
	prefix := randomString(prefixBytes)
	secret := randomString(secretBytes)
	k := Key{Prefix: prefix, Secret: secret, Full: Scheme + prefix + "_" + secret}
	return k, Hash(secret)
}

// Parse splits a presented key into its prefix and secret. It accepts only
// the exact format Generate produces, so a malformed key is rejected without
// a database lookup.
func Parse(token string) (prefix, secret string, ok bool) {
	m := format.FindStringSubmatch(token)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// Hash is what api_keys.secret_hash stores for secret.
func Hash(secret string) [32]byte {
	return sha256.Sum256([]byte(secret))
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	return strings.ToLower(encoding.EncodeToString(b))
}
