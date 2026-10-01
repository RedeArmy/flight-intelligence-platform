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

// migrateEnv is the configuration of a test database as the migrate command sees it. The migrator password comes from
// a secrets file, exactly as it does in local development.
func migrateEnv(t *testing.T, env *dbtest.Env) map[string]string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, migratorPasswordSecret), []byte(env.Password(dbtest.RoleMigrator).Reveal()), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"APP_ENV": "test", "LOG_LEVEL": "info", "LOG_FORMAT": "json",
		"POSTGRES_HOST": env.Host(), "POSTGRES_PORT": strconv.Itoa(env.Port()), "POSTGRES_DB": env.Name,
		"POSTGRES_USER": dbtest.RoleApp, "POSTGRES_MIGRATOR_USER": dbtest.RoleMigrator, "POSTGRES_SSLMODE": "disable",
		"SECRETS_DIR": dir,
	}
}

func runMigrate(t *testing.T, env map[string]string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(context.Background(), args, runOptions{Lookup: lookupOf(env), Stdout: &out})
	return out.String(), err
}

func TestMigrateCommandEndToEnd(t *testing.T) {
	db := dbtest.New(t)
	env := migrateEnv(t, db)

	out, err := runMigrate(t, env, "version")
	if err != nil || !strings.Contains(out, "version=0 dirty=false") {
		t.Fatalf("a fresh database must report version 0: %q, %v", out, err)
	}

	if out, err = runMigrate(t, env, "up"); err != nil {
		t.Fatalf("up: %v\n%s", err, out)
	}
	if !strings.Contains(out, "api_clients_keys") || !strings.Contains(out, "audit_events") {
		t.Errorf("up must log each migration it applies:\n%s", out)
	}
	if out, err = runMigrate(t, env, "up"); err != nil {
		t.Fatalf("a second up must be a no-op: %v\n%s", err, out)
	}
	if out, err = runMigrate(t, env, "version"); err != nil || !strings.Contains(out, "version=2 dirty=false") {
		t.Fatalf("after up: %q, %v", out, err)
	}

	if out, err = runMigrate(t, env, "down", "-yes", "1"); err != nil {
		t.Fatalf("down -yes 1: %v\n%s", err, out)
	}
	if out, err = runMigrate(t, env, "version"); err != nil || !strings.Contains(out, "version=1 dirty=false") {
		t.Fatalf("after down: %q, %v", out, err)
	}
	if out, err = runMigrate(t, env, "up"); err != nil {
		t.Fatalf("up after down: %v\n%s", err, out)
	}
}

func TestMigrateCommandNeverPrintsTheCredentials(t *testing.T) {
	db := dbtest.New(t)
	env := migrateEnv(t, db)
	password := db.Password(dbtest.RoleMigrator).Reveal()

	out, err := runMigrate(t, env, "up")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, password) || strings.Contains(out, "pgx5://") {
		t.Fatalf("the output leaked the password or the connection string:\n%s", out)
	}

	// A wrong database name makes the connection fail; the error must not leak credentials either.
	env["POSTGRES_DB"] = "no_such_database_for_this_test"
	_, err = runMigrate(t, env, "up")
	if err == nil {
		t.Fatal("migrating a database that does not exist must fail")
	}
	if strings.Contains(err.Error(), password) || strings.Contains(err.Error(), "pgx5://") {
		t.Fatalf("the error leaked the password or the connection string: %v", err)
	}
}

func TestMigrateCommandDownWithoutMigrationsAppliedFails(t *testing.T) {
	db := dbtest.New(t)
	if out, err := runMigrate(t, migrateEnv(t, db), "down", "-yes", "1"); err == nil {
		t.Fatalf("nothing is applied, so there is nothing to roll back:\n%s", out)
	}
}
