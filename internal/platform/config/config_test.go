package config

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func mapLookup(m map[string]string) Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func mustLoad(t *testing.T, env map[string]string) Config {
	t.Helper()
	cfg, err := Load(mapLookup(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func issuesOf(t *testing.T, err error) map[string]string {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %T: %v", err, err)
	}
	m := map[string]string{}
	for _, is := range ve.Issues {
		m[is.Key] = is.Problem
	}
	return m
}

func TestLoadDefaults(t *testing.T) {
	cfg := mustLoad(t, map[string]string{"APP_ENV": "local"})
	if cfg.App.Env != EnvLocal || cfg.App.Version != "dev" {
		t.Errorf("App = %+v", cfg.App)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "json" {
		t.Errorf("Log = %+v", cfg.Log)
	}
	h := cfg.HTTP
	if h.Addr != ":8080" || h.OperatorAddr != "127.0.0.1:8081" {
		t.Errorf("addresses = %q %q", h.Addr, h.OperatorAddr)
	}
	if h.ReadHeaderTimeout != 5*time.Second || h.ReadTimeout != 15*time.Second || h.WriteTimeout != 30*time.Second ||
		h.IdleTimeout != 60*time.Second || h.ShutdownTimeout != 25*time.Second || h.MaxBodyBytes != 1<<20 {
		t.Errorf("HTTP defaults = %+v", h)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg := mustLoad(t, map[string]string{
		"APP_ENV": "production", "APP_VERSION": "1.2.3", "LOG_LEVEL": "debug",
		"HTTP_ADDR": "0.0.0.0:9000", "HTTP_OPERATOR_ADDR": "127.0.0.1:9001",
		"HTTP_READ_TIMEOUT": "20s", "HTTP_MAX_BODY_BYTES": "2048",
	})
	if cfg.App.Env != EnvProduction || cfg.App.Version != "1.2.3" || cfg.Log.Level != "debug" {
		t.Errorf("unexpected: %+v", cfg)
	}
	if cfg.HTTP.Addr != "0.0.0.0:9000" || cfg.HTTP.ReadTimeout != 20*time.Second || cfg.HTTP.MaxBodyBytes != 2048 {
		t.Errorf("unexpected HTTP: %+v", cfg.HTTP)
	}
}

func TestEmptyAndWhitespaceValuesFallBackToDefaults(t *testing.T) {
	cfg := mustLoad(t, map[string]string{"APP_ENV": " local ", "LOG_LEVEL": "   ", "HTTP_ADDR": ""})
	if cfg.App.Env != EnvLocal || cfg.Log.Level != "info" || cfg.HTTP.Addr != ":8080" {
		t.Errorf("unexpected: %+v", cfg)
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	_, err := Load(mapLookup(map[string]string{
		"LOG_LEVEL":           "chatty",
		"HTTP_ADDR":           "not-an-address",
		"HTTP_READ_TIMEOUT":   "soon",
		"HTTP_IDLE_TIMEOUT":   "-1s",
		"HTTP_MAX_BODY_BYTES": "10",
	}))
	got := issuesOf(t, err)
	for _, key := range []string{"APP_ENV", "LOG_LEVEL", "HTTP_ADDR", "HTTP_READ_TIMEOUT", "HTTP_IDLE_TIMEOUT", "HTTP_MAX_BODY_BYTES"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing issue for %s; got %v", key, got)
		}
	}
	if got["APP_ENV"] != "is required" {
		t.Errorf("APP_ENV issue = %q", got["APP_ENV"])
	}
}

func TestValidationMessagesNeverEchoValues(t *testing.T) {
	const leaked = "hunter2-do-not-leak"
	_, err := Load(mapLookup(map[string]string{
		"APP_ENV": "local", "LOG_LEVEL": leaked, "HTTP_ADDR": leaked,
		"HTTP_READ_TIMEOUT": leaked, "HTTP_MAX_BODY_BYTES": leaked,
	}))
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), leaked) {
		t.Fatalf("error message leaked a value: %v", err)
	}
}

func TestPortValidation(t *testing.T) {
	for _, addr := range []string{":99999", ":-1", ":http", "host:", "no-port"} {
		_, err := Load(mapLookup(map[string]string{"APP_ENV": "local", "HTTP_ADDR": addr}))
		if _, ok := issuesOf(t, err)["HTTP_ADDR"]; !ok {
			t.Errorf("HTTP_ADDR=%q should be rejected", addr)
		}
	}
}

func TestPortZeroListenersAreDistinct(t *testing.T) {
	// Port 0 asks the OS for any free port, so two port-0 addresses never collide (used by tests and ephemeral runs).
	_, err := Load(mapLookup(map[string]string{"APP_ENV": "test", "HTTP_ADDR": "127.0.0.1:0", "HTTP_OPERATOR_ADDR": "127.0.0.1:0"}))
	if err != nil {
		t.Fatalf("two port-0 addresses must be accepted: %v", err)
	}
}

func TestCrossFieldRules(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		key  string
	}{
		{"text logs in production", map[string]string{"APP_ENV": "production", "LOG_FORMAT": "text"}, "LOG_FORMAT"},
		{"text logs in staging", map[string]string{"APP_ENV": "staging", "LOG_FORMAT": "text"}, "LOG_FORMAT"},
		{"same listener address", map[string]string{"APP_ENV": "local", "HTTP_ADDR": ":8080", "HTTP_OPERATOR_ADDR": ":8080"}, "HTTP_OPERATOR_ADDR"},
		{"header timeout above read timeout", map[string]string{"APP_ENV": "local", "HTTP_READ_HEADER_TIMEOUT": "20s", "HTTP_READ_TIMEOUT": "10s"}, "HTTP_READ_HEADER_TIMEOUT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(mapLookup(tc.env))
			if _, ok := issuesOf(t, err)[tc.key]; !ok {
				t.Fatalf("expected an issue for %s, got %v", tc.key, err)
			}
		})
	}
	if _, err := Load(mapLookup(map[string]string{"APP_ENV": "local", "LOG_FORMAT": "text"})); err != nil {
		t.Errorf("text logs must be allowed in local: %v", err)
	}
}

func TestEnvHelpers(t *testing.T) {
	if !EnvStaging.IsProductionLike() || !EnvProduction.IsProductionLike() || EnvLocal.IsProductionLike() {
		t.Error("IsProductionLike wrong")
	}
	if !EnvLocal.AllowsLocalFeatures() || !EnvTest.AllowsLocalFeatures() || EnvProduction.AllowsLocalFeatures() || Env("").AllowsLocalFeatures() {
		t.Error("AllowsLocalFeatures wrong")
	}
}

func TestLookupFromEnvReadsProcessEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_ADDR", ":9999")
	cfg, err := Load(LookupFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.Env != EnvTest || cfg.HTTP.Addr != ":9999" {
		t.Errorf("unexpected %+v", cfg)
	}
}

// docKeys extracts `KEY` entries from the first column of the configuration reference table.
func docKeys(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "operations", "configuration.md"))
	if err != nil {
		t.Fatalf("configuration reference missing: %v", err)
	}
	re := regexp.MustCompile("(?m)^\\| `([A-Z][A-Z0-9_]*)` \\|")
	keys := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		keys[m[1]] = true
	}
	return keys
}

func TestEveryKeyIsDocumentedAndNothingIsStale(t *testing.T) {
	documented := docKeys(t)
	code := map[string]bool{}
	for _, k := range Keys() {
		code[k] = true
		if !documented[k] {
			t.Errorf("key %s is read by the loader but missing from docs/operations/configuration.md", k)
		}
	}
	for k := range documented {
		if !code[k] {
			t.Errorf("key %s is documented but not read by the loader", k)
		}
	}
	if len(code) == 0 {
		t.Fatal("Keys() returned nothing")
	}
}

func TestEnvExampleIsValidAndComplete(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	look, err := LookupWithDotEnv(filepath.Join(root, ".env.example"), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(look); err != nil {
		t.Fatalf(".env.example must load cleanly: %v", err)
	}
}

func TestPostgresDefaults(t *testing.T) {
	cfg := mustLoad(t, map[string]string{"APP_ENV": "local", "POSTGRES_SSLMODE": "disable"})
	p := cfg.Postgres
	if p.Host != "localhost" || p.Port != 5432 || p.Name != "fip" || p.User != "fip_app" || p.MigratorUser != "fip_migrator" {
		t.Errorf("identity defaults = %+v", p)
	}
	if p.MaxConns != 10 || p.MinConns != 0 || p.ConnectTimeout != 5*time.Second ||
		p.StatementTimeout != 15*time.Second || p.MaxConnLifetime != 30*time.Minute {
		t.Errorf("pool defaults = %+v", p)
	}
	if cfg.Secrets.Dir != "" {
		t.Errorf("SECRETS_DIR must default to empty, got %q", cfg.Secrets.Dir)
	}
}

func TestPostgresDefaultsToStrictTLS(t *testing.T) {
	if cfg := mustLoad(t, map[string]string{"APP_ENV": "local"}); cfg.Postgres.SSLMode != "verify-full" {
		t.Fatalf("the default sslmode must be verify-full, got %q", cfg.Postgres.SSLMode)
	}
}

func TestPostgresCrossFieldRules(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		key  string
	}{
		{"min above max", map[string]string{"APP_ENV": "local", "POSTGRES_MIN_CONNS": "20", "POSTGRES_MAX_CONNS": "5"}, "POSTGRES_MIN_CONNS"},
		{"disabled TLS in production", map[string]string{"APP_ENV": "production", "POSTGRES_SSLMODE": "disable"}, "POSTGRES_SSLMODE"},
		{"disabled TLS in staging", map[string]string{"APP_ENV": "staging", "POSTGRES_SSLMODE": "disable"}, "POSTGRES_SSLMODE"},
		{"runtime role equals migrator role", map[string]string{"APP_ENV": "local", "POSTGRES_USER": "same", "POSTGRES_MIGRATOR_USER": "same"}, "POSTGRES_MIGRATOR_USER"},
		{"bad sslmode value", map[string]string{"APP_ENV": "local", "POSTGRES_SSLMODE": "require"}, "POSTGRES_SSLMODE"},
		{"port out of range", map[string]string{"APP_ENV": "local", "POSTGRES_PORT": "70000"}, "POSTGRES_PORT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(mapLookup(tc.env))
			if _, ok := issuesOf(t, err)[tc.key]; !ok {
				t.Fatalf("expected an issue for %s, got %v", tc.key, err)
			}
		})
	}
	prod := map[string]string{"APP_ENV": "production", "POSTGRES_SSLMODE": "verify-full"}
	if _, err := Load(mapLookup(prod)); err != nil {
		t.Errorf("verify-full must be accepted in production: %v", err)
	}
}

func TestRedisAndRateLimitDefaults(t *testing.T) {
	cfg := mustLoad(t, map[string]string{"APP_ENV": "local"})
	if cfg.Redis.Addr != "" || !cfg.Redis.TLS || cfg.Redis.Timeout != 100*time.Millisecond {
		t.Errorf("redis defaults = %+v: Redis must be off by default, with TLS on and a short timeout", cfg.Redis)
	}
	if l := cfg.Limits; l.IPPerMinute != 300 || l.ClientPerMinute != 600 || l.AuthFailuresPerMinute != 10 {
		t.Errorf("limit defaults = %+v", l)
	}
	cfg = mustLoad(t, map[string]string{"APP_ENV": "local", "REDIS_ADDR": "cache.internal:6380", "REDIS_TLS": "false", "RATE_LIMIT_IP_PER_MIN": "50"})
	if cfg.Redis.Addr != "cache.internal:6380" || cfg.Redis.TLS || cfg.Limits.IPPerMinute != 50 {
		t.Errorf("explicit values = %+v %+v", cfg.Redis, cfg.Limits)
	}
}

func TestRedisAndRateLimitRules(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		key  string
	}{
		{"address without port", map[string]string{"APP_ENV": "local", "REDIS_ADDR": "cache.internal"}, "REDIS_ADDR"},
		{"bad tls value", map[string]string{"APP_ENV": "local", "REDIS_TLS": "maybe"}, "REDIS_TLS"},
		{"plaintext Redis in production", map[string]string{"APP_ENV": "production", "POSTGRES_SSLMODE": "verify-full", "REDIS_ADDR": "cache:6379", "REDIS_TLS": "false"}, "REDIS_TLS"},
		{"zero limit", map[string]string{"APP_ENV": "local", "RATE_LIMIT_CLIENT_PER_MIN": "0"}, "RATE_LIMIT_CLIENT_PER_MIN"},
		{"non-numeric limit", map[string]string{"APP_ENV": "local", "RATE_LIMIT_AUTH_FAILURES_PER_MIN": "many"}, "RATE_LIMIT_AUTH_FAILURES_PER_MIN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(mapLookup(tc.env))
			if _, ok := issuesOf(t, err)[tc.key]; !ok {
				t.Fatalf("expected an issue for %s, got %v", tc.key, err)
			}
		})
	}
	// Without Redis configured, the TLS flag is irrelevant even in production.
	if _, err := Load(mapLookup(map[string]string{"APP_ENV": "production", "POSTGRES_SSLMODE": "verify-full", "REDIS_TLS": "false"})); err != nil {
		t.Errorf("REDIS_TLS only matters when REDIS_ADDR is set: %v", err)
	}
}
