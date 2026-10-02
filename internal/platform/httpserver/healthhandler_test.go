package httpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func healthWith(checks ...Check) *Health { return NewHealth(time.Second, checks...) }

func TestHealthHandlerReportsLivenessAndReadiness(t *testing.T) {
	tl := newTestLog(t)
	ok := Check{Name: "postgres", Critical: true, Run: func(context.Context) error { return nil }}
	down := Check{Name: "postgres", Critical: true, Run: func(context.Context) error { return errors.New("down") }}
	soft := Check{Name: "cache", Critical: false, Run: func(context.Context) error { return errors.New("slow") }}

	cases := map[string]struct {
		health *Health
		path   string
		status int
		body   []string
	}{
		"alive":            {healthWith(down), "/healthz", http.StatusOK, []string{`"status":"ok"`}},
		"ready":            {healthWith(ok), "/readyz", http.StatusOK, []string{`"status":"ready"`, `"postgres":"ok"`}},
		"degraded":         {healthWith(ok, soft), "/readyz", http.StatusOK, []string{`"status":"degraded"`, `"cache":"failed"`}},
		"not ready":        {healthWith(down), "/readyz", http.StatusServiceUnavailable, []string{`"status":"not_ready"`, `"postgres":"failed"`}},
		"no checks":        {healthWith(), "/readyz", http.StatusOK, []string{`"status":"ready"`}},
		"unknown route":    {healthWith(ok), "/v1/whoami", http.StatusNotFound, []string{`NOT_FOUND`}},
		"no API surface":   {healthWith(ok), "/v1/anything", http.StatusNotFound, nil},
		"metrics are none": {healthWith(ok), "/metrics", http.StatusNotFound, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := NewHealthHandler(HealthDeps{Logger: tl.Logger, Health: c.health})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
			if rec.Code != c.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, c.status, rec.Body.String())
			}
			for _, want := range c.body {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("body lacks %q: %s", want, rec.Body.String())
				}
			}
			if rec.Header().Get("X-Request-Id") == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("the base chain (request ID, security headers) must apply: %v", rec.Header())
			}
		})
	}
}

func TestHealthHandlerOnlyAnswersGET(t *testing.T) {
	h := NewHealthHandler(HealthDeps{Logger: newTestLog(t).Logger, Health: healthWith()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestHealthHandlerMatchesTheAPIProbesBodyForBody(t *testing.T) {
	tl := newTestLog(t)
	ok := Check{Name: "postgres", Critical: true, Run: func(context.Context) error { return nil }}
	health := healthWith(ok)
	api := NewPublicHandler(PublicDeps{Logger: tl.Logger, Health: health, MaxBodyBytes: 1 << 10})
	worker := NewHealthHandler(HealthDeps{Logger: tl.Logger, Health: health})
	for _, path := range []string{"/healthz", "/readyz"} {
		a, w := httptest.NewRecorder(), httptest.NewRecorder()
		api.ServeHTTP(a, httptest.NewRequest(http.MethodGet, path, nil))
		worker.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if a.Code != w.Code || a.Body.String() != w.Body.String() {
			t.Errorf("%s differs: API %d %q, health handler %d %q", path, a.Code, a.Body.String(), w.Code, w.Body.String())
		}
	}
}

// TestSingleListenerServerRunsAndDrains runs a Server without an operator handler (as the worker does): it binds one
// listener, reports no operator address, flips readiness to not ready as soon as shutdown starts, and stops cleanly.
func TestSingleListenerServerRunsAndDrains(t *testing.T) {
	tl := newTestLog(t)
	health := healthWith(Check{Name: "postgres", Critical: true, Run: func(context.Context) error { return nil }})
	addrs := make(chan [2]net.Addr, 1)
	srv, err := New(Options{
		Logger: tl.Logger, Public: NewHealthHandler(HealthDeps{Logger: tl.Logger, Health: health}), Health: health,
		PublicAddr: "127.0.0.1:0", OperatorAddr: "127.0.0.1:1", // the operator address must be ignored
		ReadHeaderTimeout: time.Second, ShutdownTimeout: 3 * time.Second,
		OnListening: func(p, o net.Addr) { addrs <- [2]net.Addr{p, o} },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { res <- srv.Run(ctx) }()

	var got [2]net.Addr
	select {
	case got = <-addrs:
	case err := <-res:
		t.Fatalf("stopped early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("did not start")
	}
	if got[1] != nil {
		t.Fatalf("operator address = %v, want nil for a single listener", got[1])
	}
	if code, body := get(t, "http://"+got[0].String()+"/readyz"); code != http.StatusOK || !strings.Contains(body, "ready") {
		t.Fatalf("readyz = %d %s", code, body)
	}

	cancel()
	select {
	case err := <-res:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop")
	}
	if !health.Draining() {
		t.Fatal("shutdown must flip the health object to draining")
	}
	if !strings.Contains(tl.Raw(), "http listener started") {
		t.Errorf("the single-listener start must be logged:\n%s", tl.Raw())
	}
}
