package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

const (
	leakedPassword = "hunter2-correct-horse"
	leakedBearer   = "eyJhbGciOiJIUzI1NiJ9.payload.signature"
	leakedPepper   = "pepper-value-do-not-log"
)

// leakedFIP has the platform API-key shape but is assembled from low-entropy parts, so no secret-looking
// literal exists in the source for secret scanners to flag.
var leakedFIP = "fip_abcd1234_" + strings.Repeat("x", 24)

func newTestLogger(t *testing.T, opts Options) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	opts.Out = &buf
	l, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return l, &buf
}

func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not valid JSON: %v\n%s", err, line)
		}
		out = append(out, m)
	}
	return out
}

// TestNoSecretReachesTheOutput logs known secrets through every path the platform uses (E1-3 acceptance).
func TestNoSecretReachesTheOutput(t *testing.T) {
	l, buf := newTestLogger(t, Options{Service: "api", Env: "test", Version: "1", Level: "debug"})
	ctx := WithRequestID(context.Background(), "req_1")

	l.InfoContext(ctx, "calling provider with password="+leakedPassword+" and key "+leakedFIP)
	l.InfoContext(ctx, "header", "Authorization", "Bearer "+leakedBearer)
	l.InfoContext(ctx, "attr value", "note", "token Bearer "+leakedBearer+" in text")
	l.InfoContext(ctx, "denied keys", "password", leakedPassword, "api_key", leakedFIP, "X-Api-Key", leakedFIP,
		"set-cookie", "session="+leakedPassword, "client.secret", leakedPassword, "pepper", leakedPepper)
	l.InfoContext(ctx, "secret type", "value", secret.Secret(leakedPepper))
	l.InfoContext(ctx, "nested group", slog.Group("provider", slog.String("password", leakedPassword), slog.String("name", "x")))
	l.InfoContext(ctx, "dsn", "url", "postgres://app:"+leakedPassword+"@db:5432/fip")
	l.ErrorContext(ctx, "failed", "err", errors.New("connect postgres://app:"+leakedPassword+"@db failed, key "+leakedFIP))
	l.With("password", leakedPassword).InfoContext(ctx, "with-attrs")
	l.WithGroup("g").With(slog.String("api_key", leakedFIP)).InfoContext(ctx, "group-with-attrs")
	l.InfoContext(ctx, "formatted", "detail", fmt.Sprintf("%v", fmt.Errorf("wrapped: %w", errors.New("Bearer "+leakedBearer))))

	out := buf.String()
	for _, leaked := range []string{leakedPassword, leakedFIP, leakedBearer, leakedPepper} {
		if strings.Contains(out, leaked) {
			t.Errorf("output contains secret %q:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Error("expected redaction markers in the output")
	}
}

func TestOrdinaryFieldsAreKept(t *testing.T) {
	l, buf := newTestLogger(t, Options{Service: "api", Env: "test", Version: "1.2.3"})
	l.Info("served", "route", "/v1/whoami", "status", 200, "duration_ms", 12)
	rec := lines(t, buf)[0]
	for k, want := range map[string]any{
		"service": "api", "env": "test", "version": "1.2.3", "msg": "served", "route": "/v1/whoami",
	} {
		if rec[k] != want {
			t.Errorf("%s = %v, want %v", k, rec[k], want)
		}
	}
	if rec["status"] != float64(200) || rec["level"] != "INFO" {
		t.Errorf("unexpected record %v", rec)
	}
}

func TestContextCorrelationAndTraceIDs(t *testing.T) {
	trace := func(context.Context) (string, string) { return "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7" }
	l, buf := newTestLogger(t, Options{Service: "api", Env: "test", Version: "1", Trace: trace})

	ctx := WithCorrelationID(WithRequestID(context.Background(), "req_9"), "run_5")
	l.InfoContext(ctx, "with ids")
	l.Info("without context")

	recs := lines(t, buf)
	if recs[0]["request_id"] != "req_9" || recs[0]["correlation_id"] != "run_5" ||
		recs[0]["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" || recs[0]["span_id"] != "00f067aa0ba902b7" {
		t.Errorf("context attributes missing: %v", recs[0])
	}
	if _, ok := recs[1]["request_id"]; ok {
		t.Errorf("request_id must be absent without context: %v", recs[1])
	}
	if RequestID(ctx) != "req_9" || CorrelationID(ctx) != "run_5" || RequestID(context.Background()) != "" {
		t.Error("context accessors misbehave")
	}
}

func TestTraceExtractorWithoutTraceAddsNothing(t *testing.T) {
	l, buf := newTestLogger(t, Options{Service: "api", Trace: func(context.Context) (string, string) { return "", "" }})
	l.Info("no trace")
	if _, ok := lines(t, buf)[0]["trace_id"]; ok {
		t.Error("trace_id must be omitted when there is no active trace")
	}
}

func TestLevelFiltering(t *testing.T) {
	l, buf := newTestLogger(t, Options{Service: "api", Level: "warn"})
	l.Info("hidden")
	l.Warn("shown")
	recs := lines(t, buf)
	if len(recs) != 1 || recs[0]["msg"] != "shown" {
		t.Fatalf("unexpected records: %v", recs)
	}
}

func TestTextFormatAlsoRedacts(t *testing.T) {
	l, buf := newTestLogger(t, Options{Service: "api", Format: "text"})
	l.Info("text mode", "password", leakedPassword, "note", "key "+leakedFIP)
	if strings.Contains(buf.String(), leakedPassword) || strings.Contains(buf.String(), leakedFIP) {
		t.Fatalf("text output leaked: %s", buf.String())
	}
	if strings.HasPrefix(strings.TrimSpace(buf.String()), "{") {
		t.Error("text format should not be JSON")
	}
}

func TestNewRejectsBadOptions(t *testing.T) {
	if _, err := New(Options{Level: "chatty"}); err == nil {
		t.Error("unknown level must be rejected")
	}
	if _, err := New(Options{Format: "xml"}); err == nil {
		t.Error("unknown format must be rejected")
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{"": slog.LevelInfo, "info": slog.LevelInfo, "debug": slog.LevelDebug, "warn": slog.LevelWarn, "error": slog.LevelError}
	for in, want := range cases {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v", in, got, err)
		}
	}
}

func TestScrub(t *testing.T) {
	cases := map[string]string{
		"no secrets here":                          "no secrets here",
		"login failed password=" + leakedPassword:  "login failed password=[REDACTED]",
		"token: abc123xyz and more":                "token: [REDACTED] and more",
		`{"api_key": "` + leakedFIP + `"}`:         `{"api_key": "[REDACTED]"}`,
		"password is required":                     "password is required",
		"tokens used: 5":                           "tokens used: 5",
		"key " + leakedFIP:                         "key [REDACTED]",
		"Authorization: Bearer " + leakedBearer:    "Authorization: Bearer [REDACTED]",
		"basic dXNlcjpwYXNzd29yZA==":               "basic [REDACTED]",
		"postgres://u:" + leakedPassword + "@h/db": "postgres://u:[REDACTED]@h/db",
		"https://example.com/path?x=1":             "https://example.com/path?x=1",
		"ftp://anonymous@example.com":              "ftp://anonymous@example.com",
	}
	for in, want := range cases {
		if got := Scrub(in); got != want {
			t.Errorf("Scrub(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

func TestIsSensitiveKey(t *testing.T) {
	for _, k := range []string{"password", "Password", "api_key", "X-Api-Key", "apiKey", "Authorization", "set-cookie", "client.secret", "refresh_token", "pepper", "private-key", "db_credentials"} {
		if !isSensitiveKey(k) {
			t.Errorf("%q should be sensitive", k)
		}
	}
	for _, k := range []string{"msg", "route", "status", "request_id", "provider", "duration_ms", "client_id"} {
		if isSensitiveKey(k) {
			t.Errorf("%q should not be sensitive", k)
		}
	}
}
