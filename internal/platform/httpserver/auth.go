package httpserver

import (
	"net/http"
	"strings"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/access"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

// Authenticator resolves the caller of a request. Implementations return a *sharederrors.Error of kind
// Unauthenticated for bad credentials and Unavailable when a backing store is down; the API-key
// implementation arrives with slice S4 (ADR-016, ADR-027).
type Authenticator interface {
	Authenticate(r *http.Request) (access.Principal, error)
}

// ErrUnauthenticated is the single answer for every authentication failure, so responses never reveal
// whether a key prefix exists, expired or was revoked (SR-23).
var ErrUnauthenticated = sharederrors.Unauthenticated(sharederrors.CodeUnauthenticated, "authentication required")

// ErrForbidden is returned when the caller lacks the permission a route requires.
var ErrForbidden = sharederrors.Forbidden(sharederrors.CodeForbidden, "operation not permitted")

// DenyAll is the default Authenticator: it rejects every request, so protected routes are closed until a real
// authenticator is configured.
type DenyAll struct{}

// Authenticate always fails.
func (DenyAll) Authenticate(*http.Request) (access.Principal, error) {
	return access.Principal{}, ErrUnauthenticated
}

// BearerToken extracts the token from an "Authorization: Bearer <token>" header.
func BearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
