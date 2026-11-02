package httpserver

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/problem"
)

// MaxBodyBytes is the largest request body the API accepts (1 MiB, see
// docs/design/api.md, "Request size").
const MaxBodyBytes = 1 << 20

type ctxKey int

const requestIDKey ctxKey = iota

// RequestID returns the ID that requestID middleware gave this request, or ""
// outside it.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// requestID gives every request a new UUID v4, sent back in the Request-Id
// header. A Request-Id sent by the client is ignored: the ID ends up in logs
// and the audit log, so the server must be able to trust it.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newUUID()
		w.Header().Set("Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])  // crypto/rand.Read never returns an error
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 9562 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// statusWriter remembers the status code and body size for logging, and
// whether the response has started, so recovery knows if it can still send
// a 500.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logRequests writes one log line per request, and turns a panic in a handler
// into a 500 problem. The stack goes to the log only, never to the client
// (RFC 9457 §5).
func logRequests(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			id := RequestID(r.Context())

			defer func() {
				if v := recover(); v != nil {
					if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
						panic(v)
					}
					log.Error("panic", "request_id", id, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
					if sw.status == 0 {
						problem.Write(sw, problem.New(problem.Internal, id, ""))
					}
				}
				log.Info("request",
					"request_id", id,
					"method", r.Method,
					"path", r.URL.Path,
					"status", sw.status,
					"bytes", sw.bytes,
					"duration_ms", time.Since(start).Milliseconds(),
				)
			}()

			next.ServeHTTP(sw, r)
		})
	}
}

// limitBody rejects bodies larger than MaxBodyBytes. A declared
// Content-Length over the limit is refused at once; a body without one is
// cut off by http.MaxBytesReader, and the handler that reads it maps the
// resulting *http.MaxBytesError to the same problem.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > MaxBodyBytes {
			problem.Write(w, problem.New(problem.ContentTooLarge, RequestID(r.Context()),
				fmt.Sprintf("Request bodies are limited to %d bytes.", MaxBodyBytes)))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}
