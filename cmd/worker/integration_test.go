//go:build integration

package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/dbtest"
)

// TestWorkerIsReadyWithARealDatabase starts the worker against a migrated PostgreSQL as the runtime role and checks
// that readiness turns green: the whole path from secret store to pool to readiness check works for the worker too.
func TestWorkerIsReadyWithARealDatabase(t *testing.T) {
	env := dbtest.NewMigrated(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "postgres_password"), []byte(env.Password(dbtest.RoleApp).Reveal()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]string{
		"APP_ENV": "test", "LOG_LEVEL": "error", "WORKER_HEALTH_ADDR": "127.0.0.1:0",
		"POSTGRES_HOST": env.Host(), "POSTGRES_PORT": strconv.Itoa(env.Port()), "POSTGRES_DB": env.Name,
		"POSTGRES_USER": dbtest.RoleApp, "POSTGRES_SSLMODE": "disable", "SECRETS_DIR": dir,
	}
	addr, stop := startWorker(t, cfg)

	code, body := get(t, "http://"+addr+"/readyz")
	if code != http.StatusOK || !strings.Contains(body, `"postgres":"ok"`) || !strings.Contains(body, `"ready"`) {
		t.Fatalf("readyz = %d %s; want 200 ready with postgres ok", code, body)
	}
	if err := stop(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
