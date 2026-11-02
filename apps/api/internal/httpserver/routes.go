package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/api"
	"github.com/amadeusmozartissagei/jade-invoicing/apps/api/internal/problem"
)

// handlers implements the generated strict interface. Operations are added
// slice by slice; the embedded interface is nil and only satisfies the
// compiler for the ones not written yet. Those are never routed (see
// registerV1), so their paths still answer 404.
type handlers struct {
	api.StrictServerInterface
}

// registerV1 routes the implemented /v1 operations. Each one runs
//
//	authenticate → validateRequests → generated wrapper → handler
//
// so the handler gets a decoded, contract-valid request and the tenant's
// transaction in its context.
func registerV1(mux *http.ServeMux, log *slog.Logger, pool *pgxpool.Pool) {
	h := &handlers{}
	strict := api.NewStrictHandlerWithOptions(h, nil, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  bodyError,
		ResponseErrorHandlerFunc: responseError(log),
	})
	wrapper := &api.ServerInterfaceWrapper{Handler: strict, ErrorHandlerFunc: requestError}

	auth := authenticate(log, pool)
	validate := validateRequests(log)
	route := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, auth(validate(h)))
	}

	warmContract()
	route("GET /v1/tenant", wrapper.GetTenant)
	route("GET /v1/seller-profile", wrapper.GetSellerProfile)
	route("PUT /v1/seller-profile", wrapper.PutSellerProfile)
}

// requestError and bodyError answer a parameter or body that passed
// validation but not the generated decoding, which should not happen. The
// decoders' errors name Go types, so they are not passed on.
func requestError(w http.ResponseWriter, r *http.Request, _ error) {
	problem.Write(w, problem.New(problem.BadRequest, RequestID(r.Context()), "A path or query parameter could not be read."))
}

func bodyError(w http.ResponseWriter, r *http.Request, _ error) {
	problem.Write(w, problem.New(problem.BadRequest, RequestID(r.Context()), "The request body could not be decoded."))
}

// responseError answers a handler that returned an error: a 500 with no
// details for the client, and the cause in the log under the request ID.
func responseError(log *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		id := RequestID(r.Context())
		log.Error("handler", "request_id", id, "error", err)
		problem.Write(w, problem.New(problem.Internal, id, ""))
	}
}
