package httpserver

import (
	"context"
	"net/http"
	"strings"

	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

// Role is the authorisation role attached to an API client (ADR-017).
type Role string

// Roles. USER stays dormant until user identity exists; clients carry the others today.
const (
	RoleUser      Role = "USER"
	RoleDeveloper Role = "DEVELOPER"
	RoleOperator  Role = "OPERATOR"
	RoleAdmin     Role = "ADMIN"
	RoleService   Role = "SERVICE"
)

// Permission names an operation a role may perform, for example "whoami:read".
type Permission string

// Permissions.
const (
	PermWhoamiRead Permission = "whoami:read"
)

// rolePermissions is the static role to permission table. A role missing from it has no permissions (deny by default).
var rolePermissions = map[Role]map[Permission]bool{
	RoleUser:      {PermWhoamiRead: true},
	RoleDeveloper: {PermWhoamiRead: true},
	RoleOperator:  {PermWhoamiRead: true},
	RoleAdmin:     {PermWhoamiRead: true},
	RoleService:   {PermWhoamiRead: true},
}

// Can reports whether the role grants p.
func (r Role) Can(p Permission) bool { return rolePermissions[r][p] }

// Principal is the authenticated caller.
type Principal struct {
	ClientID string
	Role     Role
}

// Authenticator resolves the caller of a request. Implementations return a *sharederrors.Error of kind
// Unauthenticated for bad credentials and Unavailable when a backing store is down; the API-key
// implementation arrives with slice S4 (ADR-016, ADR-027).
type Authenticator interface {
	Authenticate(r *http.Request) (Principal, error)
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
func (DenyAll) Authenticate(*http.Request) (Principal, error) { return Principal{}, ErrUnauthenticated }

// BearerToken extracts the token from an "Authorization: Bearer <token>" header.
func BearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

type principalKey struct{}

func withPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the authenticated principal stored in ctx by the policy middleware.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
