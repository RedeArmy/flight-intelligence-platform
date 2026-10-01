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

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/dbtest"
)

// TestReadyzWithARealDatabase starts the API against a real, migrated PostgreSQL as the runtime role and checks that
// readiness turns green: the whole path from secret store to pool to readiness check works.
func TestReadyzWithARealDatabase(t *testing.T) {
	env := dbtest.NewMigrated(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "postgres_password"), []byte(env.Password(dbtest.RoleApp).Reveal()), 0o600); err != nil {
		t.Fatal(err)
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

	var public net.Addr
	select {
	case public = <-addrs:
	case err := <-res:
		t.Fatalf("api ended early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("api did not start")
	}

	code, body := httpGetBody(t, "http://"+public.String()+"/readyz")
	if code != http.StatusOK || !strings.Contains(body, `"postgres":"ok"`) || !strings.Contains(body, `"ready"`) {
		t.Fatalf("readyz = %d %s; want 200 ready with postgres ok", code, body)
	}

	cancel()
	select {
	case err := <-res:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("api did not stop")
	}
}
