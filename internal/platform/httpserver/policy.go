package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

// routePolicy says who may call a route. Either public, or a permission the caller's role must grant.
type routePolicy struct {
	public     bool
	permission Permission
}

// routePolicies is the authorisation table, keyed by "METHOD /route/pattern". It is deny-by-default: a registered route
// that is missing here is refused at runtime, and a test checks this table against the OpenAPI contract in both
// directions (every operation has exactly one policy, public routes are exactly those with `security: []`, and
// every protected route's `x-permission` matches).
var routePolicies = map[string]routePolicy{
	"GET /healthz":   {public: true},
	"GET /readyz":    {public: true},
	"GET /v1/whoami": {permission: PermWhoamiRead},
}

// CodeRoutePolicyMissing marks a route that was registered without a policy (a programming error).
const CodeRoutePolicyMissing = "ROUTE_POLICY_MISSING"

func routeKey(r *http.Request) string {
	pattern := ""
	if rc := chi.RouteContext(r.Context()); rc != nil {
		pattern = rc.RoutePattern()
	}
	return r.Method + " " + pattern
}

// enforcePolicy authenticates and authorises the request according to its route policy and stores the principal
// in the context for handlers.
func enforcePolicy(auth Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			policy, ok := routePolicies[routeKey(r)]
			if !ok {
				WriteError(w, r, sharederrors.Internal(CodeRoutePolicyMissing, "route has no access policy"))
				return
			}
			if policy.public {
				next.ServeHTTP(w, r)
				return
			}
			principal, err := authorize(auth, r, policy.permission)
			if err != nil {
				if sharederrors.KindOf(err) == sharederrors.KindUnauthenticated {
					w.Header().Set("WWW-Authenticate", "Bearer")
				}
				WriteError(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal)))
		})
	}
}

// authorize authenticates the request and checks that the caller's role grants the permission.
func authorize(auth Authenticator, r *http.Request, permission Permission) (Principal, error) {
	principal, err := auth.Authenticate(r)
	if err != nil {
		return Principal{}, err
	}
	if !principal.Role.Can(permission) {
		return Principal{}, ErrForbidden
	}
	return principal, nil
}
