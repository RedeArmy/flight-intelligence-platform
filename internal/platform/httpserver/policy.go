package httpserver

import (
	"context"
	stderrors "errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/telemetry"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/ratelimit"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

// routePolicy says who may call a route. Either public, or a permission the caller's role must grant.
type routePolicy struct {
	public     bool
	permission Permission
	opClass    string // rate-limit class: read, search or verify; empty means read
}

// defaultOpClass is the rate-limit class of a route that does not name one.
const defaultOpClass = "read"

// class returns the operation class used to bucket the client's rate limit.
func (p routePolicy) class() string {
	if p.opClass == "" {
		return defaultOpClass
	}
	return p.opClass
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

// RateLimiter is the port the access policy uses for rate limiting. platform/ratelimit provides the adapters.
type RateLimiter interface {
	Allow(ctx context.Context, key string, rule ratelimit.Rule, cost int) (ratelimit.Decision, error)
}

// Limits are the rules the access policy enforces (ADR-032).
type Limits struct {
	IP          ratelimit.Rule // all requests from one address, before authentication
	Client      ratelimit.Rule // requests of one client and operation class
	AuthFailure ratelimit.Rule // failed authentications from one address
}

// Auditor records security-relevant request outcomes in the audit trail (SR-12).
type Auditor interface {
	Record(ctx context.Context, e AuditEvent) error
}

// AuditEvent is one entry for the audit trail.
type AuditEvent struct {
	Action    string
	ClientID  string
	Resource  string
	RequestID string
	Outcome   string // success | failure | denied
}

// ActionAuthzDenied is recorded when an authenticated caller lacks the permission a route requires.
const ActionAuthzDenied = "authz.denied"

// CodeRateLimited is the error code of every rate-limit response.
const CodeRateLimited = "RATE_LIMITED"

// guard authenticates, authorises and rate-limits protected routes. A nil limiter or auditor disables that part
// (unit tests); production wiring always sets both.
type guard struct {
	auth    Authenticator
	limiter RateLimiter
	limits  Limits
	auditor Auditor
	metrics *telemetry.Metrics // nil records nothing
}

// Names of the limits, used as the "limit" label of rate_limited_total.
const (
	limitIP          = "ip"
	limitAuthFailure = "auth_failure"
	limitClient      = "client"
)

// enforcePolicy applies the route policy table. It is deny-by-default and stores the principal in the context.
func enforcePolicy(g guard) func(http.Handler) http.Handler {
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
			principal, err := g.protect(r, policy)
			if err != nil {
				writeDenied(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal)))
		})
	}
}

// protect runs the checks of a protected route in order: address limit, failed-authentication limit (checked before
// the key is verified, so guessing is throttled), authentication, authorisation, then the client limit.
func (g guard) protect(r *http.Request, policy routePolicy) (Principal, error) {
	ctx := r.Context()
	ip := remoteHost(r.RemoteAddr)
	if err := g.take(ctx, limitIP, "ip:"+ip, g.limits.IP, 1); err != nil {
		return Principal{}, err
	}
	if err := g.take(ctx, limitAuthFailure, "authfail:"+ip, g.limits.AuthFailure, 0); err != nil {
		return Principal{}, err
	}
	principal, err := g.auth.Authenticate(r)
	if err != nil {
		if sharederrors.KindOf(err) == sharederrors.KindUnauthenticated {
			g.chargeFailure(ctx, "authfail:"+ip)
		}
		return Principal{}, err
	}
	if !principal.Role.Can(policy.permission) {
		g.auditDenied(r, principal)
		return Principal{}, ErrForbidden
	}
	if err := g.take(ctx, limitClient, "client:"+principal.ClientID+":"+policy.class(), g.limits.Client, 1); err != nil {
		return Principal{}, err
	}
	return principal, nil
}

// take charges a bucket and turns a denial into a rate-limited error. A limiter failure is logged and lets the
// request through: the Fallback limiter already absorbs Redis outages, so an error here is a bug, and refusing all
// traffic because of it would be worse than a missed limit.
func (g guard) take(ctx context.Context, limit, key string, rule ratelimit.Rule, cost int) error {
	if g.limiter == nil || !rule.Valid() {
		return nil
	}
	d, err := g.limiter.Allow(ctx, key, rule, cost)
	if err != nil {
		loggerFrom(ctx).ErrorContext(ctx, "rate limiter failed", "error", err)
		return nil
	}
	if d.Allowed {
		return nil
	}
	g.metrics.RateLimited(ctx, limit)
	return newRateLimitError(d)
}

// chargeFailure takes one token from the failed-authentication bucket. Whether a token was available does not matter:
// the caller already failed and gets its 401 either way, and the refusal of later attempts is counted when they are
// refused, not here. A limiter error is logged like in take.
func (g guard) chargeFailure(ctx context.Context, key string) {
	if g.limiter == nil || !g.limits.AuthFailure.Valid() {
		return
	}
	if _, err := g.limiter.Allow(ctx, key, g.limits.AuthFailure, 1); err != nil {
		loggerFrom(ctx).ErrorContext(ctx, "rate limiter failed", "error", err)
	}
}

func (g guard) auditDenied(r *http.Request, p Principal) {
	if g.auditor == nil {
		return
	}
	ctx := r.Context()
	err := g.auditor.Record(ctx, AuditEvent{
		Action: ActionAuthzDenied, ClientID: p.ClientID, Resource: routeKey(r),
		RequestID: logging.RequestID(ctx), Outcome: "denied",
	})
	if err != nil {
		loggerFrom(ctx).ErrorContext(ctx, "audit write failed", "action", ActionAuthzDenied, "error", err)
	}
}

// rateLimitError is the 429 error; it carries the decision so the response can include the RateLimit-* headers.
type rateLimitError struct {
	cause    *sharederrors.Error
	decision ratelimit.Decision
}

func newRateLimitError(d ratelimit.Decision) error {
	return &rateLimitError{
		cause: sharederrors.RateLimited(CodeRateLimited, "too many requests").
			WithDetails(map[string]any{"retryAfterSeconds": d.RetryAfterSeconds()}),
		decision: d,
	}
}

func (e *rateLimitError) Error() string { return e.cause.Error() }

// Unwrap lets errors.As find the platform error.
func (e *rateLimitError) Unwrap() error { return e.cause }

// writeDenied writes the response for a failed protect call.
func writeDenied(w http.ResponseWriter, r *http.Request, err error) {
	var rl *rateLimitError
	if stderrors.As(err, &rl) {
		h := w.Header()
		h.Set("RateLimit-Limit", strconv.Itoa(rl.decision.Limit))
		h.Set("RateLimit-Remaining", strconv.Itoa(rl.decision.Remaining))
		h.Set("RateLimit-Reset", strconv.Itoa(rl.decision.ResetSeconds()))
	}
	if sharederrors.KindOf(err) == sharederrors.KindUnauthenticated {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	WriteError(w, r, err)
}
