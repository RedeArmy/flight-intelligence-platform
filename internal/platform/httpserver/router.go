package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver/openapi"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

// PublicDeps are the dependencies of the public API handler.
type PublicDeps struct {
	Logger       *slog.Logger
	Auth         Authenticator // nil means DenyAll: protected routes stay closed
	Limiter      RateLimiter   // nil disables rate limiting (tests only)
	Limits       Limits
	Auditor      Auditor // nil disables auditing of authorisation denials (tests only)
	Health       *Health
	MaxBodyBytes int64
}

// NewPublicHandler builds the public API handler from the OpenAPI contract. The middleware order is:
// request ID, access log, panic recovery, security headers, body limit, then per route the access policy.
// Access log sits outside recovery so a recovered panic is logged as a 500.
func NewPublicHandler(d PublicDeps) http.Handler {
	auth := d.Auth
	if auth == nil {
		auth = DenyAll{}
	}
	r := newBaseRouter(d.Logger)
	r.Use(bodyLimit(d.MaxBodyBytes))

	strict := openapi.NewStrictHandlerWithOptions(&api{health: d.Health}, nil, openapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: WriteError,
	})
	openapi.HandlerWithOptions(strict, openapi.ChiServerOptions{
		BaseRouter:       r,
		Middlewares:      []openapi.MiddlewareFunc{enforcePolicy(guard{auth: auth, limiter: d.Limiter, limits: d.Limits, auditor: d.Auditor})},
		ErrorHandlerFunc: badRequest,
	})
	return r
}

// OperatorDeps are the dependencies of the operator handler.
type OperatorDeps struct {
	Logger *slog.Logger
	// Routes registers operator-only routes (metrics, admin). Nil registers none.
	Routes func(chi.Router)
}

// NewOperatorHandler builds the handler for the operator listener. It shares the base chain but serves none of the
// public routes, so operator routes can never be reached through the public port (SR-21).
func NewOperatorHandler(d OperatorDeps) http.Handler {
	r := newBaseRouter(d.Logger)
	if d.Routes != nil {
		d.Routes(r)
	}
	return r
}

func newBaseRouter(logger *slog.Logger) *chi.Mux {
	r := chi.NewRouter()
	r.Use(requestID(logger), accessLog, recoverer, securityHeaders)
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		WriteError(w, req, sharederrors.NotFound(sharederrors.CodeNotFound, "resource not found"))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		writeJSONError(w, req, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed", nil)
	})
	return r
}

// badRequest handles request decoding and parameter binding failures without echoing the parser's message.
func badRequest(w http.ResponseWriter, r *http.Request, err error) {
	WriteError(w, r, sharederrors.Wrap(sharederrors.KindInvalid, sharederrors.CodeInvalidRequest, "invalid request", err))
}
