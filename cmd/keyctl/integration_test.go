//go:build integration

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/dbtest"
)

func keyctlEnv(t *testing.T, env *dbtest.Env) map[string]string {
	t.Helper()
	dir := t.TempDir()
	for name, value := range map[string]string{
		adminPasswordSecret: env.Password(dbtest.RoleAdmin).Reveal(),
		pepperSecret:        strings.Repeat("test-pepper-", 4),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return map[string]string{
		"APP_ENV": "test", "LOG_LEVEL": "error",
		"POSTGRES_HOST": env.Host(), "POSTGRES_PORT": strconv.Itoa(env.Port()), "POSTGRES_DB": env.Name,
		"POSTGRES_USER": dbtest.RoleApp, "POSTGRES_ADMIN_USER": dbtest.RoleAdmin, "POSTGRES_SSLMODE": "disable",
		"SECRETS_DIR": dir,
	}
}

func runKeyctl(t *testing.T, env map[string]string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(context.Background(), args, runOptions{Lookup: lookupOf(env), Stdout: &out})
	return out.String(), err
}

func TestKeyctlEndToEnd(t *testing.T) {
	db := dbtest.NewMigrated(t)
	env := keyctlEnv(t, db)

	if out, err := runKeyctl(t, env, "client", "create", "-name", "web", "-role", "DEVELOPER"); err != nil || !strings.Contains(out, "client created") {
		t.Fatalf("client create: %q, %v", out, err)
	}
	if _, err := runKeyctl(t, env, "client", "create", "-name", "web", "-role", "DEVELOPER"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a duplicate client must be refused: %v", err)
	}

	out, err := runKeyctl(t, env, "key", "issue", "-client", "web")
	if err != nil {
		t.Fatalf("key issue: %v", err)
	}
	var prefix, token string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "key issued prefix="); ok {
			prefix, _, _ = strings.Cut(p, " ")
		}
		if v, ok := strings.CutPrefix(line, "token="); ok {
			token = v
		}
	}
	if len(prefix) != 8 || !strings.HasPrefix(token, "fip_"+prefix+"_") {
		t.Fatalf("issue output did not carry a prefix and a matching token:\n%s", out)
	}

	listed, err := runKeyctl(t, env, "key", "list")
	if err != nil || !strings.Contains(listed, prefix) || !strings.Contains(listed, "status=active") {
		t.Fatalf("key list: %q, %v", listed, err)
	}
	if strings.Contains(listed, token) || strings.Contains(listed, strings.TrimPrefix(token, "fip_"+prefix+"_")) {
		t.Fatal("key list must never show secret material")
	}

	if out, err = runKeyctl(t, env, "key", "revoke", "-prefix", prefix); err != nil || !strings.Contains(out, "key revoked") {
		t.Fatalf("revoke: %q, %v", out, err)
	}
	if listed, _ = runKeyctl(t, env, "key", "list"); !strings.Contains(listed, "status=revoked") {
		t.Fatalf("the key must be listed as revoked:\n%s", listed)
	}
	if _, err := runKeyctl(t, env, "key", "revoke", "-prefix", "Zzzzzzzz"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("revoking an unknown key: %v", err)
	}
	if _, err := runKeyctl(t, env, "key", "issue", "-client", "ghost"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("issuing for an unknown client: %v", err)
	}
}
