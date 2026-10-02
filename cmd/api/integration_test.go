//go:build integration

package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/apiauth"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/dbtest"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/security"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

// testPepper is a generated test value, long enough for the hasher.
var testPepper = secret.Secret(strings.Repeat("test-pepper-", 4))

// startAPI starts the API against a migrated test database as the runtime role and returns its public base URL.
// The API stops when the test ends.
func startAPI(t *testing.T, env *dbtest.Env, extra map[string]string) string {
	t.Helper()
	base, _ := startStoppableAPI(t, env, extra)
	return base
}

// startStoppableAPI is startAPI that also returns a stop function, for tests that need the API to shut down (and
// flush its telemetry) before they look at the result. stop is idempotent and also runs when the test ends.
func startStoppableAPI(t *testing.T, env *dbtest.Env, extra map[string]string) (base string, stop func()) {
	t.Helper()
	dir := t.TempDir()
	for name, value := range map[string]string{
		"postgres_password": env.Password(dbtest.RoleApp).Reveal(),
		"api_key_pepper":    testPepper.Reveal(),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := map[string]string{
		"APP_ENV": "test", "LOG_LEVEL": "error", "HTTP_ADDR": "127.0.0.1:0", "HTTP_OPERATOR_ADDR": "127.0.0.1:0",
		"POSTGRES_HOST": env.Host(), "POSTGRES_PORT": strconv.Itoa(env.Port()), "POSTGRES_DB": env.Name,
		"POSTGRES_USER": dbtest.RoleApp, "POSTGRES_SSLMODE": "disable", "SECRETS_DIR": dir,
	}

	for k, v := range extra {
		cfg[k] = v
	}

	addrs := make(chan net.Addr, 1)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		res <- run(ctx, runOptions{Lookup: lookupOf(cfg), Stdout: io.Discard, OnListening: func(p, _ net.Addr) { addrs <- p }})
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-res:
				if err != nil {
					t.Errorf("shutdown: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Error("api did not stop")
			}
		})
	}
	t.Cleanup(stop)

	select {
	case public := <-addrs:
		return "http://" + public.String(), stop
	case err := <-res:
		t.Fatalf("api ended early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("api did not start")
	}
	return "", stop
}

// TestReadyzWithARealDatabase starts the API against a real, migrated PostgreSQL as the runtime role and checks that
// readiness turns green: the whole path from secret store to pool to readiness check works.
func TestReadyzWithARealDatabase(t *testing.T) {
	base := startAPI(t, dbtest.NewMigrated(t), nil)
	code, body := httpGetBody(t, base+"/readyz")
	if code != http.StatusOK || !strings.Contains(body, `"postgres":"ok"`) || !strings.Contains(body, `"ready"`) {
		t.Fatalf("readyz = %d %s; want 200 ready with postgres ok", code, body)
	}
}

func whoami(t *testing.T, base, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/v1/whoami", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestAPIKeyLifecycleEndToEnd issues a key as the operator role and uses it against the running API: it works, a
// tampered or unknown key does not, and a revoked key stops working at once. Every failure looks the same (SR-23).
func TestAPIKeyLifecycleEndToEnd(t *testing.T) {
	env := dbtest.NewMigrated(t)
	base := startAPI(t, env, nil)

	pool, err := database.Open(context.Background(), env.Config(dbtest.RoleAdmin))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	hasher, err := security.NewKeyHasher(testPepper)
	if err != nil {
		t.Fatal(err)
	}
	admin := apiauth.NewAdmin(pool, hasher, clock.System{})
	ctx := context.Background()
	if _, err := admin.CreateClient(ctx, "e2e-client", httpserver.RoleDeveloper); err != nil {
		t.Fatal(err)
	}
	key, err := admin.IssueKey(ctx, "e2e-client", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if code, body := whoami(t, base, key.Token.Reveal()); code != http.StatusOK || !strings.Contains(body, "DEVELOPER") {
		t.Fatalf("valid key: %d %s", code, body)
	}

	tampered := key.Token.Reveal()[:len(key.Token.Reveal())-1] + "A"
	if tampered == key.Token.Reveal() {
		tampered = key.Token.Reveal()[:len(key.Token.Reveal())-1] + "B"
	}
	unknown := "fip_ZZZZZZZZ_" + strings.Repeat("A", 43)
	failures := map[string]string{}
	for name, token := range map[string]string{"none": "", "tampered": tampered, "unknown": unknown, "malformed": "not-a-key"} {
		code, body := whoami(t, base, token)
		if code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, code)
		}
		failures[name] = stripRequestID(body)
	}
	for name, body := range failures {
		if body != failures["none"] {
			t.Errorf("%s response differs from the missing-key response, which leaks information:\n%s\nvs\n%s", name, body, failures["none"])
		}
	}

	if err := admin.RevokeKey(ctx, key.Prefix); err != nil {
		t.Fatal(err)
	}
	if code, _ := whoami(t, base, key.Token.Reveal()); code != http.StatusUnauthorized {
		t.Fatalf("revoked key: status %d, want 401", code)
	}
}

// stripRequestID removes the per-request identifier so response bodies can be compared.
func stripRequestID(body string) string {
	var out []string
	for _, part := range strings.Split(body, ",") {
		if !strings.Contains(part, "request_id") && !strings.Contains(part, "requestId") {
			out = append(out, part)
		}
	}
	return strings.Join(out, ",")
}

// redisEnv returns the settings that point the API at the test Redis, or skips when none is configured.
// When TEST_REDIS_ADDR is set an unreachable Redis fails the test instead of skipping it.
func redisEnv(t *testing.T, limits map[string]string) map[string]string {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR is not set")
	}
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("TEST_REDIS_ADDR is set but Redis is unreachable: %v", err)
	}
	_ = conn.Close()
	// A generous Redis timeout: these tests are about the limiter working through Redis, not about timeouts. With the
	// production default of 100 ms, a busy machine can make one Redis call slow enough for the limiter to fall back to
	// its stricter local limits (ADR-032), which is correct behaviour and would make the test fail for the wrong reason.
	// The outage test sets its own short timeout.
	env := map[string]string{"REDIS_ADDR": addr, "REDIS_TLS": "false", "REDIS_TIMEOUT": "2s"}
	for k, v := range limits {
		env[k] = v
	}
	return env
}

func issueKey(t *testing.T, env *dbtest.Env, client string, role httpserver.Role) string {
	t.Helper()
	pool, err := database.Open(context.Background(), env.Config(dbtest.RoleAdmin))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	hasher, err := security.NewKeyHasher(testPepper)
	if err != nil {
		t.Fatal(err)
	}
	admin := apiauth.NewAdmin(pool, hasher, clock.System{})
	if _, err := admin.CreateClient(context.Background(), client, role); err != nil {
		t.Fatal(err)
	}
	key, err := admin.IssueKey(context.Background(), client, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return key.Token.Reveal()
}

// TestRateLimitingWithRedisEndToEnd proves the whole chain on a running API: the client limit answers 429 with
// Retry-After and RateLimit-* headers, readiness reports Redis, and probes are never limited.
func TestRateLimitingWithRedisEndToEnd(t *testing.T) {
	env := dbtest.NewMigrated(t)
	base := startAPI(t, env, redisEnv(t, map[string]string{"RATE_LIMIT_CLIENT_PER_MIN": "3"}))
	token := issueKey(t, env, "limited", httpserver.RoleDeveloper)

	for i := range 3 {
		if code, body := whoami(t, base, token); code != http.StatusOK {
			t.Fatalf("request %d: %d %s", i, code, body)
		}
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/v1/whoami", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the fourth request must be limited, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" || resp.Header.Get("RateLimit-Limit") != "3" || resp.Header.Get("RateLimit-Remaining") != "0" {
		t.Fatalf("headers = %v", resp.Header)
	}

	code, body := httpGetBody(t, base+"/readyz")
	if code != http.StatusOK || !strings.Contains(body, `"redis":"ok"`) {
		t.Fatalf("readyz = %d %s; want ready with redis ok", code, body)
	}
}

// TestRateLimitingSurvivesARedisOutage points the API at a closed port: it must still start, stay ready but
// degraded, and keep limiting from local memory with stricter limits (ADR-004).
func TestRateLimitingSurvivesARedisOutage(t *testing.T) {
	env := dbtest.NewMigrated(t)
	base := startAPI(t, env, map[string]string{
		"REDIS_ADDR": "127.0.0.1:1", "REDIS_TLS": "false", "REDIS_TIMEOUT": "100ms", "RATE_LIMIT_CLIENT_PER_MIN": "4",
	})
	token := issueKey(t, env, "outage", httpserver.RoleDeveloper)

	ok := 0
	for range 6 {
		if code, _ := whoami(t, base, token); code == http.StatusOK {
			ok++
		}
	}
	if ok != 2 {
		t.Fatalf("%d requests passed, want exactly 2 (half of the limit of 4 while Redis is down)", ok)
	}
	code, body := httpGetBody(t, base+"/readyz")
	if code != http.StatusOK || !strings.Contains(body, "degraded") || !strings.Contains(body, `"redis":"failed"`) {
		t.Fatalf("readyz = %d %s; want ready but degraded with redis failed", code, body)
	}
}

// TestAuthorisationDenialsReachTheAuditLog calls a route with a role that lacks the permission and checks the
// audit row written by the runtime role. The built-in permission table grants whoami to every role, so the role is
// changed in the database to one the table does not know.
func TestAuthorisationDenialsReachTheAuditLog(t *testing.T) {
	env := dbtest.NewMigrated(t)
	base := startAPI(t, env, nil)
	token := issueKey(t, env, "denied", httpserver.RoleDeveloper)

	ctx := context.Background()
	conn := env.Super(ctx)
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "ALTER TABLE api_clients DROP CONSTRAINT api_clients_role_valid"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "UPDATE api_clients SET role = 'GHOST' WHERE name = 'denied'"); err != nil {
		t.Fatal(err)
	}

	if code, _ := whoami(t, base, token); code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", code)
	}
	var n int
	err := conn.QueryRow(ctx,
		"SELECT count(*) FROM audit_events WHERE action = 'authz.denied' AND outcome = 'denied' AND resource_id = 'GET /v1/whoami' AND actor_client_id IS NOT NULL AND request_id LIKE 'req_%'").Scan(&n)
	if err != nil || n != 1 {
		t.Fatalf("audit rows = %d, err = %v; want exactly one authz.denied row", n, err)
	}
}
