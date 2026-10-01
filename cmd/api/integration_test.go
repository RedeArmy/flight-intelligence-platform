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
func startAPI(t *testing.T, env *dbtest.Env) string {
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

	addrs := make(chan net.Addr, 1)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		res <- run(ctx, runOptions{Lookup: lookupOf(cfg), Stdout: io.Discard, OnListening: func(p, _ net.Addr) { addrs <- p }})
	}()
	t.Cleanup(func() {
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

	select {
	case public := <-addrs:
		return "http://" + public.String()
	case err := <-res:
		t.Fatalf("api ended early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("api did not start")
	}
	return ""
}

// TestReadyzWithARealDatabase starts the API against a real, migrated PostgreSQL as the runtime role and checks that
// readiness turns green: the whole path from secret store to pool to readiness check works.
func TestReadyzWithARealDatabase(t *testing.T) {
	base := startAPI(t, dbtest.NewMigrated(t))
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
	base := startAPI(t, env)

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
