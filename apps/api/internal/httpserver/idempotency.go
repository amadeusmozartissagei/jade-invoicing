package httpserver

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/problem"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/store"
)

// maxIdempotencyKey is the longest key, without the quotes (api.md).
const maxIdempotencyKey = 255

// replayedHeaders are the response headers stored with a key and sent again
// on a replay. Request-Id is not one of them: every request gets its own.
var replayedHeaders = []string{"Content-Type", "Location", "ETag"}

// idempotent makes a POST safe to retry (docs/design/api.md, "Idempotency";
// ADR 0008 A3, ADR 0014). It runs inside authenticate, so every step below
// shares the request's tenant transaction:
//
//  1. Read Idempotency-Key: missing gives 400 idempotency_key_required, a
//     malformed one 400 bad_request. Then read the body with the same
//     Content-Type, size, and JSON checks as validation (415, 413, 400).
//  2. Take a transaction-scoped advisory lock on (tenant, key) without
//     waiting. If another request holds it: 409 idempotency_key_in_use.
//  3. Look the key up. Same fingerprint (method, path, body bytes): replay
//     the stored response with Idempotent-Replayed: true. Different
//     fingerprint: 422 idempotency_key_reused. Expired: delete it and go on.
//  4. Run the rest (validation, handler). On a 2xx, store the response in the
//     same transaction, so the change and its key commit together or not at
//     all. Errors are not stored: the request changed nothing, and the client
//     may retry with the same key.
//
// It runs before request validation because a missing key must get its own
// code, and because a replayed request was valid when it was first sent.
func idempotent(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			id := RequestID(ctx)

			values := r.Header.Values("Idempotency-Key")
			if len(values) == 0 {
				problem.Write(w, problem.New(problem.IdempotencyKeyRequired, id,
					"Send an Idempotency-Key header with every POST; a UUID v4 is recommended."))
				return
			}
			key, ok := "", len(values) == 1
			if ok {
				key, ok = parseIdempotencyKey(values[0])
			}
			if !ok {
				problem.Write(w, problem.New(problem.BadRequest, id,
					"Idempotency-Key must be one value of 1 to 255 printable ASCII characters, optionally in double quotes."))
				return
			}

			// Content-Type, size, and JSON are checked before any replay, so a
			// request that would fail them fails the same way with a stored key.
			body, ok := readBody(w, r)
			if !ok {
				return
			}
			hash := fingerprint(r.Method, r.URL.Path, body)

			q := queries(ctx)
			locked, err := q.TryIdempotencyLock(ctx, key)
			if err != nil {
				internalError(w, log, id, "idempotency lock", err)
				return
			}
			if !locked {
				w.Header().Set("Retry-After", "1")
				problem.Write(w, problem.New(problem.IdempotencyKeyInUse, id,
					"A request with this Idempotency-Key is being processed. Retry to get its result."))
				return
			}

			stored, err := q.GetIdempotencyKey(ctx, key)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				internalError(w, log, id, "read idempotency key", err)
				return
			case stored.Expired:
				if err := q.DeleteIdempotencyKey(ctx, key); err != nil {
					internalError(w, log, id, "delete expired idempotency key", err)
					return
				}
			case subtle.ConstantTimeCompare(stored.RequestHash, hash[:]) != 1:
				problem.Write(w, problem.New(problem.IdempotencyKeyReused, id,
					"This Idempotency-Key was used for a different request. Use a new key for a new request."))
				return
			default:
				if err := replay(w, stored); err != nil {
					internalError(w, log, id, "replay idempotency key", err)
				}
				return
			}

			buf := newBufferWriter()
			next.ServeHTTP(buf, r)
			if s := buf.Status(); s >= 200 && s < 300 {
				headers, err := json.Marshal(keptHeaders(buf.Header()))
				if err == nil {
					err = q.InsertIdempotencyKey(ctx, store.InsertIdempotencyKeyParams{
						Key:             key,
						Method:          r.Method,
						Path:            r.URL.Path,
						RequestHash:     hash[:],
						ResponseStatus:  int16(s), //nolint:gosec // 200 to 299
						ResponseHeaders: string(headers),
						ResponseBody:    buf.body.Bytes(),
					})
				}
				if err != nil {
					internalError(w, log, id, "store idempotency key", err)
					return
				}
			}
			buf.flushTo(w)
		})
	}
}

// parseIdempotencyKey accepts the IETF draft's form, a Structured Field
// string ("..." with \" and \\ escapes), and a bare value, which many clients
// send. Both name the same key: "abc" and abc are one key.
func parseIdempotencyKey(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, `"`) {
		if len(v) < 2 || !strings.HasSuffix(v, `"`) {
			return "", false
		}
		var b strings.Builder
		inner := v[1 : len(v)-1]
		for i := 0; i < len(inner); i++ {
			switch c := inner[i]; c {
			case '\\':
				i++
				if i == len(inner) || (inner[i] != '"' && inner[i] != '\\') {
					return "", false
				}
				b.WriteByte(inner[i])
			case '"':
				return "", false
			default:
				b.WriteByte(c)
			}
		}
		v = b.String()
	}
	if len(v) == 0 || len(v) > maxIdempotencyKey {
		return "", false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return "", false
		}
	}
	return v, true
}

// fingerprint is SHA-256 over the method, the path, and the raw body. Each
// part is length-prefixed, so no two different requests share an encoding.
func fingerprint(method, path string, body []byte) [32]byte {
	h := sha256.New()
	for _, part := range [][]byte{[]byte(method), []byte(path), body} {
		_, _ = fmt.Fprintf(h, "%d:", len(part))
		_, _ = h.Write(part)
	}
	var sum [32]byte
	h.Sum(sum[:0])
	return sum
}

func keptHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for _, name := range replayedHeaders {
		if v := h.Get(name); v != "" {
			out[name] = v
		}
	}
	return out
}

// replay sends a stored response again, byte for byte.
func replay(w http.ResponseWriter, stored store.GetIdempotencyKeyRow) error {
	var headers map[string]string
	if err := json.Unmarshal(stored.ResponseHeaders, &headers); err != nil {
		return err
	}
	for name, v := range headers {
		w.Header().Set(name, v)
	}
	w.Header().Set("Idempotent-Replayed", "true")
	w.WriteHeader(int(stored.ResponseStatus))
	_, err := w.Write(stored.ResponseBody)
	return err
}

func internalError(w http.ResponseWriter, log *slog.Logger, requestID, msg string, err error) {
	log.Error(msg, "request_id", requestID, "error", err)
	problem.Write(w, problem.New(problem.Internal, requestID, ""))
}
