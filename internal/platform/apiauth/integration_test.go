//go:build integration

package apiauth_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/apiauth"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/dbtest"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/security"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type rig struct {
	admin  *apiauth.Admin
	auth   *apiauth.Authenticator
	clock  *clock.Fake
	env    *dbtest.Env
	hasher *security.KeyHasher
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)) }

// newRig migrates a throw-away database and connects as the operator role (for Admin) and as the runtime role
// (for the Authenticator), exactly as production does.
func newRig(t *testing.T) *rig {
	t.Helper()
	env := dbtest.NewMigrated(t)
	ctx := context.Background()
	adminPool, err := database.Open(ctx, env.Config(dbtest.RoleAdmin))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	appPool, err := database.Open(ctx, env.Config(dbtest.RoleApp))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(appPool.Close)
	hasher, err := security.NewKeyHasher(secret.Secret(strings.Repeat("pepper-", 6)))
	if err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFake(t0)
	return &rig{
		admin:  apiauth.NewAdmin(adminPool, hasher, fake),
		auth:   apiauth.New(apiauth.NewPGStore(appPool), hasher, fake, quietLogger()),
		clock:  fake,
		env:    env,
		hasher: hasher,
	}
}

func (r *rig) authenticate(token string) (httpserver.Principal, error) {
	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return r.auth.Authenticate(req)
}

// exec runs a statement as superuser, bypassing every grant.
func (r *rig) exec(t *testing.T, sql string) {
	t.Helper()
	ctx := context.Background()
	conn := r.env.Super(ctx)
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	ctx := context.Background()
	conn := r.env.Super(ctx)
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIssuedKeyAuthenticatesAndExpires(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	client, err := r.admin.CreateClient(ctx, "svc-a", httpserver.RoleService)
	if err != nil {
		t.Fatal(err)
	}
	key, err := r.admin.IssueKey(ctx, "svc-a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	p, err := r.authenticate(key.Token.Reveal())
	if err != nil || p.ClientID != client.ID || p.Role != httpserver.RoleService {
		t.Fatalf("principal = %+v, err = %v", p, err)
	}

	r.clock.Advance(time.Hour)
	if _, err := r.authenticate(key.Token.Reveal()); sharederrors.KindOf(err) != sharederrors.KindUnauthenticated {
		t.Fatalf("an expired key must be unauthenticated, got %v", err)
	}
}

func TestRevokedKeyStopsWorkingAndRevokingTwiceKeepsTheFirstTime(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.admin.CreateClient(ctx, "svc-b", httpserver.RoleDeveloper); err != nil {
		t.Fatal(err)
	}
	key, err := r.admin.IssueKey(ctx, "svc-b", 0)
	if err != nil {
		t.Fatal(err)
	}
	if key.ExpiresAt != nil {
		t.Error("ttl 0 means no expiry")
	}

	if err := r.admin.RevokeKey(ctx, key.Prefix); err != nil {
		t.Fatal(err)
	}
	first := r.clock.Now()
	r.clock.Advance(time.Hour)
	if err := r.admin.RevokeKey(ctx, key.Prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := r.authenticate(key.Token.Reveal()); sharederrors.KindOf(err) != sharederrors.KindUnauthenticated {
		t.Fatalf("revoked key: %v", err)
	}
	keys, err := r.admin.ListKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].RevokedAt == nil || !keys[0].RevokedAt.Equal(first) {
		t.Fatalf("keys = %+v, err = %v; want the first revocation time %v kept", keys, err, first)
	}
}

func TestInactiveClientCannotAuthenticate(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.admin.CreateClient(ctx, "svc-c", httpserver.RoleDeveloper); err != nil {
		t.Fatal(err)
	}
	key, err := r.admin.IssueKey(ctx, "svc-c", 0)
	if err != nil {
		t.Fatal(err)
	}
	r.exec(t, "UPDATE api_clients SET active = false WHERE name = 'svc-c'")
	if _, err := r.authenticate(key.Token.Reveal()); sharederrors.KindOf(err) != sharederrors.KindUnauthenticated {
		t.Fatalf("an inactive client must be unauthenticated, got %v", err)
	}
}

func TestEveryChangeWritesAnAuditEventAndNeverTheSecret(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.admin.CreateClient(ctx, "svc-d", httpserver.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	key, err := r.admin.IssueKey(ctx, "svc-d", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.admin.RevokeKey(ctx, key.Prefix); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{apiauth.ActionClientCreated, apiauth.ActionKeyIssued, apiauth.ActionKeyRevoked} {
		if n := r.count(t, "SELECT count(*) FROM audit_events WHERE action = $1 AND outcome = 'success'", action); n != 1 {
			t.Errorf("%s: %d audit rows, want 1", action, n)
		}
	}
	_, keySecret, _ := security.ParseKey(key.Token.Reveal())
	if n := r.count(t, "SELECT count(*) FROM audit_events WHERE position($1 in details::text) > 0", keySecret); n != 0 {
		t.Fatal("the key secret must never reach the audit log")
	}
}

func TestAdminErrors(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.admin.CreateClient(ctx, "dup", httpserver.RoleUser); err != nil {
		t.Fatal(err)
	}
	create := func(name string, role httpserver.Role) func() error {
		return func() error { _, err := r.admin.CreateClient(ctx, name, role); return err }
	}
	issue := func(name string, ttl time.Duration) func() error {
		return func() error { _, err := r.admin.IssueKey(ctx, name, ttl); return err }
	}
	cases := []struct {
		name string
		do   func() error
		code string
	}{
		{"duplicate client", create("dup", httpserver.RoleUser), apiauth.CodeClientExists},
		{"empty name", create("", httpserver.RoleUser), apiauth.CodeInvalidName},
		{"long name", create(strings.Repeat("x", 129), httpserver.RoleUser), apiauth.CodeInvalidName},
		{"unknown role", create("x", httpserver.Role("ROOT")), apiauth.CodeInvalidRole},
		{"key for unknown client", issue("nobody", 0), apiauth.CodeClientNotFound},
		{"negative ttl", issue("dup", -time.Second), apiauth.CodeInvalidTTL},
		{"revoke unknown prefix", func() error { return r.admin.RevokeKey(ctx, "Zzzzzzzz") }, apiauth.CodeKeyNotFound},
	}
	for _, c := range cases {
		if got := sharederrors.CodeOf(c.do()); got != c.code {
			t.Errorf("%s: code %q, want %q", c.name, got, c.code)
		}
	}
	// A failed change leaves no audit row behind.
	if n := r.count(t, "SELECT count(*) FROM audit_events"); n != 1 {
		t.Errorf("only the one successful change should be audited, got %d rows", n)
	}
}

func TestStoreFailureMapsToUnavailable(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	pool, err := database.Open(context.Background(), r.env.Config(dbtest.RoleApp))
	if err != nil {
		t.Fatal(err)
	}
	pool.Close() // a closed pool fails every call
	auth := apiauth.New(apiauth.NewPGStore(pool), r.hasher, r.clock, quietLogger())
	key, err := r.hasher.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+key.Token.Reveal())
	if _, err := auth.Authenticate(req); sharederrors.KindOf(err) != sharederrors.KindUnavailable {
		t.Fatalf("err = %v, want unavailable", err)
	}
}
