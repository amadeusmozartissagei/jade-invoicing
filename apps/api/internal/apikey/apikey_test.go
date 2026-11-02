package apikey

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestGenerateParsesBack(t *testing.T) {
	k, hash := Generate()

	if len(k.Prefix) != 16 || len(k.Secret) != 52 {
		t.Fatalf("prefix %d chars, secret %d chars; want 16 and 52", len(k.Prefix), len(k.Secret))
	}
	if k.Full != "jade_sk_"+k.Prefix+"_"+k.Secret {
		t.Errorf("Full = %q", k.Full)
	}
	prefix, secret, ok := Parse(k.Full)
	if !ok || prefix != k.Prefix || secret != k.Secret {
		t.Fatalf("Parse(%q) = %q, %q, %v", k.Full, prefix, secret, ok)
	}
	if hash != sha256.Sum256([]byte(k.Secret)) || hash != Hash(k.Secret) {
		t.Error("hash is not SHA-256 of the secret part")
	}
}

func TestGenerateIsRandom(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		k, _ := Generate()
		if seen[k.Prefix] || seen[k.Secret] {
			t.Fatal("Generate repeated a prefix or secret")
		}
		seen[k.Prefix], seen[k.Secret] = true, true
	}
}

func TestParseRejects(t *testing.T) {
	k, _ := Generate()
	cases := map[string]string{
		"empty":          "",
		"no scheme":      k.Prefix + "_" + k.Secret,
		"other scheme":   "jade_pk_" + k.Prefix + "_" + k.Secret,
		"uppercase":      strings.ToUpper(k.Full),
		"short prefix":   "jade_sk_" + k.Prefix[1:] + "_" + k.Secret,
		"long secret":    k.Full + "a",
		"short secret":   k.Full[:len(k.Full)-1],
		"no separator":   "jade_sk_" + k.Prefix + k.Secret,
		"extra part":     k.Full + "_" + k.Secret,
		"not base32":     "jade_sk_" + k.Prefix + "_" + strings.Repeat("1", 52),
		"padding":        "jade_sk_" + k.Prefix + "_" + k.Secret[:51] + "=",
		"spaces":         " " + k.Full,
		"trailing space": k.Full + " ",
	}
	for name, token := range cases {
		if _, _, ok := Parse(token); ok {
			t.Errorf("%s: Parse(%q) accepted", name, token)
		}
	}
}
