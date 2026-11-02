package httpserver

import (
	"bytes"
	"net/http"
)

// bufferWriter holds a handler's response until its transaction has
// committed, so a client never sees a success for a change that was rolled
// back (ADR 0012). Responses are small JSON documents; nothing streams.
type bufferWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferWriter() *bufferWriter {
	return &bufferWriter{header: http.Header{}}
}

func (b *bufferWriter) Header() http.Header { return b.header }

func (b *bufferWriter) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *bufferWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// Status is the status the handler wrote, 200 if it only wrote a body, and 0
// if it wrote nothing.
func (b *bufferWriter) Status() int { return b.status }

// flushTo sends the buffered response. Headers already set on w (Request-Id)
// are kept unless the handler set the same one.
func (b *bufferWriter) flushTo(w http.ResponseWriter) {
	for k, v := range b.header {
		w.Header()[k] = v
	}
	status := b.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(b.body.Bytes())
}
