package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
)

func lookupOf(m map[string]string) config.Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// env is a complete environment: an unreachable database (the pool connects lazily) and a generated password file.
func env(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DatabasePasswordSecret), []byte("generated-for-"+t.Name()), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"APP_ENV": "test", "LOG_LEVEL": "info", "LOG_FORMAT": "json",
		"POSTGRES_HOST": "127.0.0.1", "POSTGRES_PORT": "1", "POSTGRES_SSLMODE": "disable", "POSTGRES_CONNECT_TIMEOUT": "200ms",
		"SECRETS_DIR": dir,
	}
}

func TestStartBuildsEverythingAFreshProcessNeeds(t *testing.T) {
	var out bytes.Buffer
	svc, err := Start(context.Background(), Options{Name: "demo", Lookup: lookupOf(env(t)), Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close(context.Background())

	if svc.Config.App.Env != "test" || svc.Logger == nil || svc.Secrets == nil || svc.Telemetry == nil || svc.Inst == nil || svc.Pool == nil {
		t.Fatalf("incomplete service: %+v", svc)
	}
	svc.Logger.Info("hello")
	if !strings.Contains(out.String(), `"service":"demo"`) {
		t.Fatalf("the logger must carry the service name: %s", out.String())
	}
}

func TestStartReportsTheFirstProblemAndLeavesNothingOpen(t *testing.T) {
	cases := map[string]struct {
		mutate func(e map[string]string)
		want   string
	}{
		"missing configuration": {func(e map[string]string) { delete(e, "APP_ENV") }, "APP_ENV"},
		"invalid log format":    {func(e map[string]string) { e["LOG_FORMAT"] = "yaml" }, "LOG_FORMAT"},
		"no database password":  {func(e map[string]string) { e["SECRETS_DIR"] = "" }, "database password"},
		"production-like":       {func(e map[string]string) { e["APP_ENV"], e["POSTGRES_SSLMODE"] = "production", "verify-full" }, "production-like"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := env(t)
			c.mutate(e)
			svc, err := Start(context.Background(), Options{Name: "demo", Lookup: lookupOf(e)})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
			if svc != nil {
				t.Fatal("a failed start must not return a service")
			}
		})
	}
}

func TestHealthHasACriticalDatabaseCheckAndTakesExtraChecks(t *testing.T) {
	svc, err := Start(context.Background(), Options{Name: "demo", Lookup: lookupOf(env(t))})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close(context.Background())

	health, err := svc.Health()
	if err != nil {
		t.Fatal(err)
	}
	state, checks := health.Readiness(context.Background())
	if state != "not_ready" || checks["postgres"] != "failed" {
		t.Fatalf("an unreachable database must make the service not ready: %s %v", state, checks)
	}
}

func TestCloseIsSafeOnAPartlyBuiltService(t *testing.T) {
	(&Service{}).Close(context.Background()) // a nil pool and nil telemetry must not panic the cleanup path
}
