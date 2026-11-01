package db

import (
	"crypto/subtle"
	"net/http"
	"os"
)

// Authorized checks the bearer token so that strangers cannot burn the
// free-tier quota by calling the spike endpoints.
func Authorized(r *http.Request) bool {
	token := os.Getenv("SPIKE_TOKEN")
	if token == "" {
		return false
	}
	want := []byte("Bearer " + token)
	got := []byte(r.Header.Get("Authorization"))
	return subtle.ConstantTimeCompare(got, want) == 1
}
