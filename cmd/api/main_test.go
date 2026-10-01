package main

import (
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

func baseEnv() map[string]string {
	return map[string]string{
		"APP_ENV":            "test",
		"LOG_LEVEL":          "error",
		"HTTP_ADDR":          "127.0.0.1:0",
		"HTTP_OPERATOR_ADDR": "127.0.0.1:0",
	}
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
			Lookup: lookupOf(baseEnv()),
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
	if code := httpGet(t, "http://"+a[0].String()+"/readyz"); code != http.StatusOK {
		t.Errorf("readyz = %d", code)
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
	env := baseEnv()
	env["HTTP_ADDR"] = taken.Addr().String()

	err = run(context.Background(), runOptions{Lookup: lookupOf(env), Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "listen on public address") {
		t.Fatalf("run = %v", err)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
