// Package problem writes RFC 9457 problem details in the shape fixed by the
// API contract (docs/design/api.md, "Errors"; codes in docs/api/errors.md).
package problem

import (
	"encoding/json"
	"net/http"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/api"
)

// TypeBase is the prefix of every problem type URI. The code is the fragment.
// Type URIs are frozen within /v1, so this value must never change.
const TypeBase = "https://github.com/amadeusmozartissagei/jade-invoicing/blob/main/docs/api/errors.md#"

// ContentType is the media type of every error response.
const ContentType = "application/problem+json"

// Kind is one problem type from docs/api/errors.md: its code, status, and
// title, which are the same for every occurrence.
type Kind struct {
	Code   string
	Status int
	Title  string
}

// Problem types used by the transport layer. Domain errors are added with the
// handlers that return them.
var (
	BadRequest           = Kind{"bad_request", http.StatusBadRequest, "Bad request"}
	Unauthorized         = Kind{"unauthorized", http.StatusUnauthorized, "Unauthorized"}
	Forbidden            = Kind{"forbidden", http.StatusForbidden, "Forbidden"}
	NotFound             = Kind{"not_found", http.StatusNotFound, "Not found"}
	MethodNotAllowed     = Kind{"method_not_allowed", http.StatusMethodNotAllowed, "Method not allowed"}
	ContentTooLarge      = Kind{"content_too_large", http.StatusRequestEntityTooLarge, "Content too large"}
	UnsupportedMediaType = Kind{"unsupported_media_type", http.StatusUnsupportedMediaType, "Unsupported media type"}
	ValidationFailed     = Kind{"validation_failed", http.StatusUnprocessableEntity, "Validation failed"}
	Internal             = Kind{"internal_error", http.StatusInternalServerError, "Internal error"}
)

// New builds the problem body for kind. detail may be empty.
func New(kind Kind, requestID, detail string) api.Problem {
	p := api.Problem{
		Type:      TypeBase + kind.Code,
		Code:      kind.Code,
		Title:     kind.Title,
		Status:    kind.Status,
		RequestId: requestID,
	}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

// Write sends p with its status code. Headers such as Allow or
// WWW-Authenticate must be set by the caller before calling Write.
func Write(w http.ResponseWriter, p api.Problem) {
	body, err := json.Marshal(p)
	if err != nil {
		// api.Problem has only strings, ints, and slices of them; Marshal
		// cannot fail on it.
		panic(err)
	}
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(p.Status)
	_, _ = w.Write(body)
}
