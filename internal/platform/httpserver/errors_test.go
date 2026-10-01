package httpserver

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

func TestStatusFor(t *testing.T) {
	cases := map[sharederrors.Kind]int{
		sharederrors.KindInvalid:         http.StatusBadRequest,
		sharederrors.KindUnauthenticated: http.StatusUnauthorized,
		sharederrors.KindForbidden:       http.StatusForbidden,
		sharederrors.KindNotFound:        http.StatusNotFound,
		sharederrors.KindConflict:        http.StatusConflict,
		sharederrors.KindRateLimited:     http.StatusTooManyRequests,
		sharederrors.KindUnavailable:     http.StatusServiceUnavailable,
		sharederrors.KindTimeout:         http.StatusGatewayTimeout,
		sharederrors.KindInternal:        http.StatusInternalServerError,
		sharederrors.Kind(99):            http.StatusInternalServerError,
	}
	for kind, want := range cases {
		if got := statusFor(kind); got != want {
			t.Errorf("statusFor(%v) = %d, want %d", kind, got, want)
		}
	}
}

// writeErr renders err through WriteError with a request ID and logger in the context.
func writeErr(t *testing.T, tl *testLog, err error) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil)
	ctx := withLogger(logging.WithRequestID(req.Context(), "req_test_0001"), tl.Logger)
	rec := httptest.NewRecorder()
	WriteError(rec, req.WithContext(ctx), err)
	return rec
}

func TestWriteErrorEnvelope(t *testing.T) {
	tl := newTestLog(t)
	err := sharederrors.Invalid("BAD_ORIGIN", "origin is not an airport").
		WithDetails(map[string]any{"field": "origin"})

	rec := writeErr(t, tl, err)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	got := decodeError(t, rec)
	if got.Code != "BAD_ORIGIN" || got.Message != "origin is not an airport" || got.RequestID != "req_test_0001" {
		t.Errorf("payload = %+v", got)
	}
	if got.Details["field"] != "origin" {
		t.Errorf("details = %v", got.Details)
	}
	if tl.Last("request failed") != nil {
		t.Error("client errors must not be logged as server failures")
	}
}

func TestWriteErrorInternalHidesCauseAndLogsIt(t *testing.T) {
	tl := newTestLog(t)
	cause := stderrors.New("dial postgres://app:hunter2pw@db:5432/fip: connection refused")
	err := sharederrors.Wrap(sharederrors.KindInternal, "DB_FAILURE", "db exploded", cause).
		WithDetails(map[string]any{"dsn": "postgres://app:hunter2pw@db"})

	rec := writeErr(t, tl, err)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, leaked := range []string{"db exploded", "postgres://", "hunter2pw", "connection refused", "dsn"} {
		if strings.Contains(body, leaked) {
			t.Errorf("response leaked %q: %s", leaked, body)
		}
	}
	got := decodeError(t, rec)
	if got.Code != "DB_FAILURE" || got.Message != "internal error" || got.Details != nil {
		t.Errorf("payload = %+v", got)
	}

	rec2 := tl.Last("request failed")
	if rec2 == nil || rec2["level"] != "ERROR" || rec2["code"] != "DB_FAILURE" {
		t.Fatalf("server failure not logged properly: %v", rec2)
	}
	if strings.Contains(tl.Raw(), "hunter2pw") {
		t.Errorf("log leaked the password: %s", tl.Raw())
	}
}

func TestWriteErrorUnclassifiedIsGenericInternal(t *testing.T) {
	rec := writeErr(t, newTestLog(t), stderrors.New("something with secret=abc123"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	got := decodeError(t, rec)
	if got.Code != sharederrors.CodeInternal || got.Message != "internal error" {
		t.Errorf("payload = %+v", got)
	}
	if strings.Contains(rec.Body.String(), "abc123") {
		t.Error("response leaked the error text")
	}
}

func TestWriteErrorMapsBodyTooLarge(t *testing.T) {
	rec := writeErr(t, newTestLog(t), &http.MaxBytesError{Limit: 10})
	if rec.Code != http.StatusRequestEntityTooLarge || decodeError(t, rec).Code != CodePayloadTooLarge {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestWriteErrorMapsContextDeadlineToGatewayTimeout(t *testing.T) {
	rec := writeErr(t, newTestLog(t), context.DeadlineExceeded)
	if rec.Code != http.StatusGatewayTimeout || decodeError(t, rec).Code != sharederrors.CodeTimeout {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestWriteErrorSetsRetryAfterForRateLimits(t *testing.T) {
	err := sharederrors.RateLimited(sharederrors.CodeRateLimited, "slow down").
		WithDetails(map[string]any{"retryAfterSeconds": 7})
	rec := writeErr(t, newTestLog(t), err)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "7" {
		t.Fatalf("got %d, Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	bare := writeErr(t, newTestLog(t), sharederrors.RateLimited(sharederrors.CodeRateLimited, "slow down"))
	if bare.Header().Get("Retry-After") != "" {
		t.Error("Retry-After must be absent without a hint")
	}
}

func TestWriteErrorWithoutLoggerOrRequestIDStillWorks(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	WriteError(rec, req, sharederrors.NotFound(sharederrors.CodeNotFound, "gone"))
	if rec.Code != http.StatusNotFound || decodeError(t, rec).RequestID != "" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}
