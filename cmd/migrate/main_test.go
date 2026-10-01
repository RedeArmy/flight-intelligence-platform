package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
)

func TestParseArgs(t *testing.T) {
	ok := map[string]command{
		"up":               {name: "up"},
		"version":          {name: "version"},
		"down -yes 2":      {name: "down", steps: 2, confirmed: true},
		"down -yes=true 1": {name: "down", steps: 1, confirmed: true},
	}
	for line, want := range ok {
		got, err := parseArgs(strings.Fields(line))
		if err != nil {
			t.Errorf("%q: %v", line, err)
			continue
		}
		if got != want {
			t.Errorf("%q = %+v, want %+v", line, got, want)
		}
	}
}

func TestParseArgsRejectsBadInput(t *testing.T) {
	bad := []string{
		"", "sideways", "up extra", "version now",
		"down", "down 2", "down -yes", "down -yes 0", "down -yes -1", "down -yes two", "down -yes 1 2",
		"down 3 -yes", "down -nope 1",
	}
	for _, line := range bad {
		if _, err := parseArgs(strings.Fields(line)); err == nil {
			t.Errorf("%q must be rejected", line)
		}
	}
}

func TestDownNeverRunsWithoutConfirmation(t *testing.T) {
	_, err := parseArgs([]string{"down", "1"})
	if err == nil || !strings.Contains(err.Error(), "-yes") {
		t.Fatalf("err = %v", err)
	}
}

func lookupOf(m map[string]string) config.Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestRunRefusesDownOutsideLocalEnvironments(t *testing.T) {
	env := map[string]string{"APP_ENV": "production", "POSTGRES_SSLMODE": "verify-full"}
	err := run(context.Background(), []string{"down", "-yes", "1"}, runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "outside local and test") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunNeedsTheMigratorPasswordFromTheSecretStore(t *testing.T) {
	env := map[string]string{"APP_ENV": "test"} // no SECRET_POSTGRES_MIGRATOR_PASSWORD, no SECRETS_DIR
	err := run(context.Background(), []string{"up"}, runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunRefusesProductionBecauseNoManagedSecretStoreExistsYet(t *testing.T) {
	env := map[string]string{"APP_ENV": "production", "POSTGRES_SSLMODE": "verify-full"}
	err := run(context.Background(), []string{"up"}, runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "production-like") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunRejectsInvalidConfigurationAndUsage(t *testing.T) {
	if err := run(context.Background(), []string{"up"}, runOptions{Lookup: lookupOf(map[string]string{}), Stdout: io.Discard}); err == nil ||
		!strings.Contains(err.Error(), "APP_ENV") {
		t.Errorf("missing config: %v", err)
	}
	if err := run(context.Background(), nil, runOptions{Lookup: lookupOf(nil), Stdout: io.Discard}); err == nil {
		t.Error("no arguments must be a usage error")
	}
}

func TestRealMainReturnsTheExitCodeAndPrintsTheErrorOnStderr(t *testing.T) {
	var stderr bytes.Buffer
	o := runOptions{Lookup: lookupOf(map[string]string{}), Stdout: io.Discard}

	if code := realMain(context.Background(), []string{"up"}, o, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1 for a failing command", code)
	}
	if !strings.HasPrefix(stderr.String(), "migrate: ") || !strings.Contains(stderr.String(), "APP_ENV") {
		t.Errorf("stderr = %q", stderr.String())
	}

	stderr.Reset()
	if code := realMain(context.Background(), nil, o, &stderr); code != 1 || !strings.Contains(stderr.String(), "usage") {
		t.Errorf("no arguments: code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunRejectsAnEnvFileThatSelectsProduction(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("APP_ENV=production\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), []string{"up"}, runOptions{Lookup: lookupOf(map[string]string{}), DotEnvPath: path, Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "local and test") {
		t.Fatalf("run = %v", err)
	}
}

func TestVersionAgainstAnUnreachableDatabaseFailsWithoutLeakingTheSecret(t *testing.T) {
	dir := t.TempDir()
	const password = "unit-test-generated-value-0042"
	if err := os.WriteFile(filepath.Join(dir, migratorPasswordSecret), []byte(password), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"APP_ENV": "test", "POSTGRES_HOST": "127.0.0.1", "POSTGRES_PORT": "1", "POSTGRES_SSLMODE": "disable",
		"POSTGRES_CONNECT_TIMEOUT": "300ms", "SECRETS_DIR": dir,
	}
	err := run(context.Background(), []string{"version"}, runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil {
		t.Fatal("an unreachable database must be an error")
	}
	if strings.Contains(err.Error(), password) || strings.Contains(err.Error(), "pgx5://") {
		t.Fatalf("the error leaked the password or the connection string: %v", err)
	}
}
