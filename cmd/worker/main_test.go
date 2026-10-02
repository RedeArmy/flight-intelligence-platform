package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/config"
)

func lookupOf(m map[string]string) config.Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// baseEnv is a complete environment for a unit test: a random probe port, an unreachable database (port 1) and a
// secrets directory holding a generated runtime password, so no credential literal appears in the test.
func baseEnv(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "postgres_password"), []byte("generated-"+strings.ReplaceAll(t.Name(), "/", "-")), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"APP_ENV": "test", "LOG_LEVEL": "error", "WORKER_HEALTH_ADDR": "127.0.0.1:0",
		"POSTGRES_HOST": "127.0.0.1", "POSTGRES_PORT": "1", "POSTGRES_SSLMODE": "disable", "POSTGRES_CONNECT_TIMEOUT": "200ms",
		"SECRETS_DIR": dir,
	}
}

func get(t *testing.T, url string) (int, string) {
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

// startWorker runs the worker and returns the address of its probe listener and a stop function that waits for exit.
func startWorker(t *testing.T, env map[string]string) (addr string, stop func() error) {
	t.Helper()
	addrs := make(chan net.Addr, 1)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		res <- run(ctx, runOptions{Lookup: lookupOf(env), Stdout: io.Discard, OnListening: func(a net.Addr) { addrs <- a }})
	}()
	select {
	case a := <-addrs:
		addr = a.String()
	case err := <-res:
		cancel()
		t.Fatalf("worker ended before listening: %v", err)
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("worker did not start")
	}
	var (
		once    sync.Once
		stopErr error
	)
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case stopErr = <-res:
			case <-time.After(10 * time.Second):
				t.Error("worker did not stop")
			}
		})
		return stopErr
	}
	t.Cleanup(func() { _ = stop() })
	return addr, stop
}

func TestWorkerIsAliveButNotReadyWithoutADatabase(t *testing.T) {
	addr, stop := startWorker(t, baseEnv(t))
	if code, body := get(t, "http://"+addr+"/healthz"); code != http.StatusOK || !strings.Contains(body, `"ok"`) {
		t.Fatalf("healthz = %d %s: liveness must not depend on the database", code, body)
	}
	if code, body := get(t, "http://"+addr+"/readyz"); code != http.StatusServiceUnavailable || !strings.Contains(body, "not_ready") {
		t.Fatalf("readyz = %d %s: an unreachable database must make the worker not ready", code, body)
	}
	if err := stop(); err != nil {
		t.Fatalf("a signal-driven stop must be clean: %v", err)
	}
}

func TestWorkerExposesNoAPIRoutes(t *testing.T) {
	addr, _ := startWorker(t, baseEnv(t))
	for _, path := range []string{"/v1/whoami", "/metrics", "/"} {
		if code, _ := get(t, "http://"+addr+path); code != http.StatusNotFound {
			t.Errorf("%s = %d: the worker must serve only its probes", path, code)
		}
	}
}

func TestWorkerListensOnLoopbackByDefault(t *testing.T) {
	env := baseEnv(t)
	delete(env, "WORKER_HEALTH_ADDR")
	env["WORKER_HEALTH_ADDR"] = "" // unset: the default applies, but port 8082 may be busy, so only inspect the config
	cfg, err := config.Load(lookupOf(env))
	if err != nil || !strings.HasPrefix(cfg.Worker.HealthAddr, "127.0.0.1:") {
		t.Fatalf("default probe address = %q, %v", cfg.Worker.HealthAddr, err)
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
	env["APP_ENV"], env["POSTGRES_SSLMODE"] = "production", "verify-full"
	err := run(context.Background(), runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "production-like") {
		t.Fatalf("run = %v", err)
	}
}

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	env := baseEnv(t)
	env["WORKER_HEALTH_ADDR"] = "not-an-address"
	err := run(context.Background(), runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "WORKER_HEALTH_ADDR") {
		t.Fatalf("run = %v", err)
	}
}

func TestRunFailsWhenTheProbePortIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	env := baseEnv(t)
	env["WORKER_HEALTH_ADDR"] = taken.Addr().String()
	err = run(context.Background(), runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("run = %v", err)
	}
}

func TestRealMainReturnsTheExitCodeAndPrintsTheErrorOnStderr(t *testing.T) {
	var stderr bytes.Buffer
	if code := realMain(context.Background(), runOptions{Lookup: lookupOf(map[string]string{}), Stdout: io.Discard}, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1 for a startup failure", code)
	}
	if !strings.HasPrefix(stderr.String(), "worker: ") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRealMainExitsZeroAfterACleanStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	listening := make(chan struct{})
	done := make(chan int, 1)
	var stderr bytes.Buffer
	go func() {
		done <- realMain(ctx, runOptions{
			Lookup: lookupOf(baseEnv(t)), Stdout: io.Discard, OnListening: func(net.Addr) { close(listening) },
		}, &stderr)
	}()
	select {
	case <-listening:
	case code := <-done:
		t.Fatalf("worker ended early with %d: %s", code, stderr.String())
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("a clean stop must exit 0, got %d: %s", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not stop")
	}
}
