package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
)

func lookupOf(m map[string]string) config.Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// baseEnv is a complete environment for a unit test: random ports, an unreachable database (port 1), and a secrets
// directory holding a generated runtime password, so no credential literal appears in the test.
func baseEnv(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	if err := writeFile(dir+"/postgres_password", "generated-"+strings.ReplaceAll(t.Name(), "/", "-")); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"APP_ENV":                  "test",
		"LOG_LEVEL":                "error",
		"HTTP_ADDR":                "127.0.0.1:0",
		"HTTP_OPERATOR_ADDR":       "127.0.0.1:0",
		"POSTGRES_HOST":            "127.0.0.1",
		"POSTGRES_PORT":            "1",
		"POSTGRES_SSLMODE":         "disable",
		"POSTGRES_CONNECT_TIMEOUT": "200ms",
		"SECRETS_DIR":              dir,
	}
}

func httpGetBody(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func httpGet(t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	err := run(context.Background(), runOptions{Lookup: lookupOf(map[string]string{}), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "APP_ENV: is required") {
		t.Fatalf("run = %v", err)
	}
}

func TestRunRejectsAnEnvFileThatSelectsProduction(t *testing.T) {
	// Production-like environments must never be selected from a .env file (SR-19).
	dir := t.TempDir()
	path := dir + "/.env"
	if err := writeFile(path, "APP_ENV=production\n"); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), runOptions{Lookup: lookupOf(map[string]string{}), DotEnvPath: path, Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "local and test") {
		t.Fatalf("run = %v", err)
	}
}

func TestRunServesThenStopsOnCancel(t *testing.T) {
	addrs := make(chan [2]net.Addr, 1)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		res <- run(ctx, runOptions{
			Lookup: lookupOf(baseEnv(t)),
			Stdout: io.Discard,
			OnListening: func(p, o net.Addr) {
				addrs <- [2]net.Addr{p, o}
			},
		})
	}()

	var a [2]net.Addr
	select {
	case a = <-addrs:
	case err := <-res:
		t.Fatalf("run ended early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("api did not start")
	}

	if code := httpGet(t, "http://"+a[0].String()+"/healthz"); code != http.StatusOK {
		t.Errorf("healthz = %d", code)
	}
	// The database is unreachable (port 1): the API must still start, and readiness must say so truthfully.
	code, body := httpGetBody(t, "http://"+a[0].String()+"/readyz")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, `"not_ready"`) || !strings.Contains(body, `"postgres":"failed"`) {
		t.Errorf("readyz = %d %s; want 503 not_ready with postgres failed", code, body)
	}
	// No authenticator is wired yet, so protected routes are closed (deny by default).
	if code := httpGet(t, "http://"+a[0].String()+"/v1/whoami"); code != http.StatusUnauthorized {
		t.Errorf("whoami = %d, want 401", code)
	}
	if code := httpGet(t, "http://"+a[1].String()+"/healthz"); code != http.StatusNotFound {
		t.Errorf("operator listener served a public route: %d", code)
	}

	cancel()
	select {
	case err := <-res:
		if err != nil {
			t.Fatalf("run returned %v after a clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

func TestRunReportsABindFailure(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	env := baseEnv(t)
	env["HTTP_ADDR"] = taken.Addr().String()

	err = run(context.Background(), runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "listen on public address") {
		t.Fatalf("run = %v", err)
	}
}

func TestRunFailsFastWithoutTheDatabasePassword(t *testing.T) {
	env := baseEnv(t)
	env["SECRETS_DIR"] = t.TempDir() // empty: no postgres_password
	err := run(context.Background(), runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "database password") {
		t.Fatalf("run = %v", err)
	}
}

func TestRunRefusesProductionBecauseNoManagedSecretStoreExistsYet(t *testing.T) {
	env := baseEnv(t)
	// Local secret files must never feed a production-like environment (SR-19). The TLS mode is valid here so the
	// configuration passes and the secret store is what refuses.
	env["APP_ENV"], env["POSTGRES_SSLMODE"] = "production", "verify-full"
	err := run(context.Background(), runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "production-like") {
		t.Fatalf("run = %v", err)
	}
}

func TestRunRejectsAnInsecureDatabaseModeInProduction(t *testing.T) {
	env := baseEnv(t)
	env["APP_ENV"], env["POSTGRES_SSLMODE"] = "staging", "disable"
	err := run(context.Background(), runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "POSTGRES_SSLMODE") {
		t.Fatalf("run = %v", err)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func TestRealMainReturnsTheExitCodeAndPrintsTheErrorOnStderr(t *testing.T) {
	var stderr bytes.Buffer
	if code := realMain(context.Background(), runOptions{Lookup: lookupOf(map[string]string{}), Stdout: io.Discard}, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1 for a startup failure", code)
	}
	if !strings.HasPrefix(stderr.String(), "api: ") || !strings.Contains(stderr.String(), "APP_ENV") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRealMainReturnsZeroAfterACleanStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	addrs := make(chan struct{}, 1)
	res := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		res <- realMain(ctx, runOptions{
			Lookup: lookupOf(baseEnv(t)), Stdout: io.Discard,
			OnListening: func(net.Addr, net.Addr) { addrs <- struct{}{} },
		}, &stderr)
	}()
	select {
	case <-addrs:
	case code := <-res:
		t.Fatalf("api ended early with code %d: %s", code, stderr.String())
	case <-time.After(5 * time.Second):
		t.Fatal("api did not start")
	}
	cancel()
	select {
	case code := <-res:
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("clean stop: code=%d stderr=%q", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("api did not stop")
	}
}
