// Package access holds the vocabulary of authorisation: who the caller is (Principal), which role it has (Role) and
// what a role may do (Permission). It has no HTTP or storage code, so application services can use it without importing
// the transport layer; httpserver authenticates and authorises requests with it.
package access

import "context"

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

type principalKey struct{}

// WithPrincipal returns a context that carries p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the authenticated principal stored in ctx.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
