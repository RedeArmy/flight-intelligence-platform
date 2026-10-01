package httpserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

// chainFor builds the base chain with a few probe routes.
func chainFor(tl *testLog, limit int64) *chi.Mux {
	r := newBaseRouter(tl.Logger)
	if limit > 0 {
		r.Use(bodyLimit(limit))
	}
	r.Get("/ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("kaboom password=hunter2pw") })
	r.Get("/abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	r.Post("/echo", func(w http.ResponseWriter, req *http.Request) {
		if _, err := io.ReadAll(req.Body); err != nil {
			WriteError(w, req, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return r
}

func TestRequestIDIsGeneratedAndEchoed(t *testing.T) {
	rec := doReq(chainFor(newTestLog(t), 0), http.MethodGet, "/ok")
	id := rec.Header().Get("X-Request-Id")
	if !regexp.MustCompile(`^req_[a-z2-7]{16}$`).MatchString(id) {
		t.Fatalf("generated request id = %q", id)
	}
	other := doReq(chainFor(newTestLog(t), 0), http.MethodGet, "/ok").Header().Get("X-Request-Id")
	if id == other {
		t.Error("request ids must be unique")
	}
}

func TestInboundRequestIDIsAcceptedOnlyWhenWellFormed(t *testing.T) {
	h := chainFor(newTestLog(t), 0)

	if got := doReq(h, http.MethodGet, "/ok", withHeader("X-Request-Id", "client-trace.42_ab")).Header().Get("X-Request-Id"); got != "client-trace.42_ab" {
		t.Errorf("valid inbound id was not kept: %q", got)
	}
	for _, bad := range []string{"short", strings.Repeat("a", 65), "has space in it", "semi;colon;12345", "newline\tTab12345"} {
		got := doReq(h, http.MethodGet, "/ok", withHeader("X-Request-Id", bad)).Header().Get("X-Request-Id")
		if got == bad || !strings.HasPrefix(got, "req_") {
			t.Errorf("bad inbound id %q was not replaced: %q", bad, got)
		}
	}
}

func TestRecovererReturnsGenericJSON500AndLogsThePanic(t *testing.T) {
	tl := newTestLog(t)
	rec := doReq(chainFor(tl, 0), http.MethodGet, "/boom")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := decodeError(t, rec); got.Code != sharederrors.CodeInternal || got.Message != "internal error" || got.RequestID == "" {
		t.Errorf("payload = %+v", got)
	}
	if strings.Contains(rec.Body.String(), "kaboom") {
		t.Error("response leaked the panic value")
	}
	logged := tl.Last("panic recovered")
	if logged == nil || logged["level"] != "ERROR" || logged["stack"] == nil {
		t.Fatalf("panic not logged with a stack: %v", logged)
	}
	if strings.Contains(tl.Raw(), "hunter2pw") {
		t.Error("log leaked the password inside the panic value")
	}
	access := tl.Last("request")
	if access == nil || access["status"] != float64(500) || access["level"] != "ERROR" {
		t.Errorf("access log should record the recovered 500: %v", access)
	}
}

func TestRecovererRepanicsOnAbortHandler(t *testing.T) {
	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
			t.Fatalf("recovered %v, want http.ErrAbortHandler", rec)
		}
	}()
	doReq(chainFor(newTestLog(t), 0), http.MethodGet, "/abort")
	t.Fatal("expected a panic")
}

func TestAccessLogRecordsRouteTemplateAndNothingSensitive(t *testing.T) {
	tl := newTestLog(t)
	h := chainFor(tl, 0)
	doReq(h, http.MethodGet, "/ok?token=sekret-query-value&x=1", withHeader("Authorization", "Bearer sekret-header-value-123"))
	doReq(h, http.MethodGet, "/no/such/route")

	recs := tl.Records()
	var ok, unmatched map[string]any
	for _, r := range recs {
		if r["msg"] != "request" {
			continue
		}
		switch r["route"] {
		case "/ok":
			ok = r
		case "unmatched":
			unmatched = r
		}
	}
	if ok == nil || ok["method"] != "GET" || ok["status"] != float64(200) || ok["level"] != "INFO" || ok["request_id"] == nil {
		t.Fatalf("access record = %v", ok)
	}
	if unmatched == nil || unmatched["status"] != float64(404) {
		t.Fatalf("unmatched record = %v", unmatched)
	}
	for _, secret := range []string{"sekret-query-value", "sekret-header-value"} {
		if strings.Contains(tl.Raw(), secret) {
			t.Errorf("access log leaked %q", secret)
		}
	}
}

func TestProbeTrafficIsLoggedAtDebug(t *testing.T) {
	tl := newTestLog(t)
	doReq(chainFor(tl, 0), http.MethodGet, "/healthz")
	if rec := tl.Last("request"); rec == nil || rec["level"] != "DEBUG" {
		t.Fatalf("probe request should log at DEBUG: %v", rec)
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := doReq(chainFor(newTestLog(t), 0), http.MethodGet, "/ok")
	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "no-store",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestSecurityHeadersAlsoOnErrorResponses(t *testing.T) {
	rec := doReq(chainFor(newTestLog(t), 0), http.MethodGet, "/missing")
	if rec.Code != http.StatusNotFound || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("got %d, headers %v", rec.Code, rec.Header())
	}
}

func TestBodyLimitReturns413(t *testing.T) {
	h := chainFor(newTestLog(t), 10)
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/echo", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("0123456789"); rec.Code != http.StatusNoContent {
		t.Errorf("body at the limit: status %d", rec.Code)
	}
	rec := post("0123456789X")
	if rec.Code != http.StatusRequestEntityTooLarge || decodeError(t, rec).Code != CodePayloadTooLarge {
		t.Errorf("oversized body: %d %s", rec.Code, rec.Body.String())
	}
}

func TestNotFoundAndMethodNotAllowedUseTheErrorEnvelope(t *testing.T) {
	h := chainFor(newTestLog(t), 0)

	nf := doReq(h, http.MethodGet, "/missing")
	if nf.Code != http.StatusNotFound || decodeError(t, nf).Code != sharederrors.CodeNotFound {
		t.Errorf("404: %d %s", nf.Code, nf.Body.String())
	}
	mna := doReq(h, http.MethodDelete, "/ok")
	if mna.Code != http.StatusMethodNotAllowed || decodeError(t, mna).Code != CodeMethodNotAllowed {
		t.Errorf("405: %d %s", mna.Code, mna.Body.String())
	}
	if decodeError(t, nf).RequestID == "" {
		t.Error("error envelopes must carry the request id")
	}
}
