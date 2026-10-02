//go:build integration

package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/access"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/database/dbtest"
)

// otlpSink is a stand-in OpenTelemetry collector: an OTLP/HTTP receiver that keeps what it is sent.
type otlpSink struct {
	mu     sync.Mutex
	bodies map[string][]byte
	srv    *httptest.Server
}

func newOTLPSink(t *testing.T) *otlpSink {
	t.Helper()
	s := &otlpSink{bodies: map[string][]byte{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies[r.URL.Path] = append(s.bodies[r.URL.Path], b...)
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *otlpSink) get(path string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.bodies[path]...)
}

// TestTelemetryReachesTheCollectorEndToEnd runs the real API with an OTLP endpoint, makes authenticated and rejected
// calls, stops the API (which flushes) and checks that the collector received the HTTP and database spans and the
// request, authentication-failure, pool, readiness and runtime metrics, and that no secret travelled with them.
func TestTelemetryReachesTheCollectorEndToEnd(t *testing.T) {
	sink := newOTLPSink(t)
	env := dbtest.NewMigrated(t)
	base, stop := startStoppableAPI(t, env, map[string]string{
		"TELEMETRY_OTLP_ENDPOINT": sink.srv.URL, "TELEMETRY_METRIC_INTERVAL": "1s",
	})
	token := issueKey(t, env, "traced", access.RoleDeveloper)

	if code, body := whoami(t, base, token); code != http.StatusOK {
		t.Fatalf("valid call: %d %s", code, body)
	}
	if code, _ := whoami(t, base, "not-a-key"); code != http.StatusUnauthorized {
		t.Fatalf("invalid call: %d", code)
	}
	stop()

	traces := sink.get("/v1/traces")
	for _, want := range []string{"GET /v1/whoami", "db SELECT", "request.id", "http.route", "service.name"} {
		if !bytes.Contains(traces, []byte(want)) {
			t.Errorf("traces lack %q (%d bytes received)", want, len(traces))
		}
	}
	metrics := sink.get("/v1/metrics")
	for _, want := range []string{
		"http.server.requests", "http.server.duration", "auth.failures", "malformed", "readiness.state",
		"db.pool.max_connections", "go.goroutines",
	} {
		if !bytes.Contains(metrics, []byte(want)) {
			t.Errorf("metrics lack %q (%d bytes received)", want, len(metrics))
		}
	}

	for name, payload := range map[string][]byte{"traces": traces, "metrics": metrics} {
		for _, leak := range [][]byte{[]byte(token), []byte("not-a-key"), []byte(testPepper.Reveal()), []byte("Authorization")} {
			if bytes.Contains(payload, leak) {
				t.Errorf("%s payload contains %q: secrets and credentials must never be exported", name, leak)
			}
		}
	}
}
