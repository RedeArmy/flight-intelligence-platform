package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
)

// testLog is a real redacting logger that writes JSON to an in-memory buffer.
type testLog struct {
	t      *testing.T
	buf    bytes.Buffer
	Logger *slog.Logger
}

func newTestLog(t *testing.T) *testLog {
	t.Helper()
	tl := &testLog{t: t}
	l, err := logging.New(logging.Options{Service: "test", Env: "test", Version: "0", Level: "debug", Out: &tl.buf})
	if err != nil {
		t.Fatal(err)
	}
	tl.Logger = l
	return tl
}

// Raw returns everything logged so far.
func (tl *testLog) Raw() string { return tl.buf.String() }

// Records returns the logged JSON records.
func (tl *testLog) Records() []map[string]any {
	tl.t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(tl.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			tl.t.Fatalf("log line is not JSON: %v: %s", err, line)
		}
		out = append(out, m)
	}
	return out
}

// Last returns the most recent record whose msg equals msg, or nil.
func (tl *testLog) Last(msg string) map[string]any {
	tl.t.Helper()
	recs := tl.Records()
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i]["msg"] == msg {
			return recs[i]
		}
	}
	return nil
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorPayload {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not the error envelope: %v\n%s", err, rec.Body.String())
	}
	return body.Error
}

func doReq(h http.Handler, method, target string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, nil)
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func withHeader(k, v string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(k, v) }
}

// eventually polls cond until it is true or a deadline passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
