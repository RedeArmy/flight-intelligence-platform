package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/ratelimit"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

var guardT0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type recordingAuditor struct {
	events []AuditEvent
	err    error
}

func (a *recordingAuditor) Record(_ context.Context, e AuditEvent) error {
	a.events = append(a.events, e)
	return a.err
}

type failingLimiter struct{}

func (failingLimiter) Allow(context.Context, string, ratelimit.Rule, int) (ratelimit.Decision, error) {
	return ratelimit.Decision{}, errors.New("limiter bug")
}

func perHour(n int) ratelimit.Rule { return ratelimit.Rule{Limit: n, Window: time.Hour} }

// guardedHandler is the public handler with real in-memory limiting and a fake clock.
func guardedHandler(t *testing.T, tl *testLog, auth Authenticator, limits Limits, a Auditor) (http.Handler, *clock.Fake) {
	t.Helper()
	fake := clock.NewFake(guardT0)
	h := NewPublicHandler(PublicDeps{
		Logger: tl.Logger, Auth: auth, Health: NewHealth(time.Second), MaxBodyBytes: 1 << 10,
		Limiter: ratelimit.NewMemory(fake, 0), Limits: limits, Auditor: a,
	})
	return h, fake
}

func whoamiFrom(h http.Handler, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	req.RemoteAddr = remote
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAddressLimitAnswers429WithHeadersBeforeAuthenticating(t *testing.T) {
	auth := &fakeAuth{principal: Principal{ClientID: "c1", Role: RoleDeveloper}}
	h, _ := guardedHandler(t, newTestLog(t), auth, Limits{IP: perHour(2)}, nil)

	for i := range 2 {
		if rec := whoamiFrom(h, "198.51.100.7:4000"); rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	callsBefore := auth.calls
	rec := whoamiFrom(h, "198.51.100.7:4001") // same address, another port
	if rec.Code != http.StatusTooManyRequests || decodeError(t, rec).Code != CodeRateLimited {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if auth.calls != callsBefore {
		t.Fatal("a limited request must not reach authentication")
	}
	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retry < 1 {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	if rec.Header().Get("RateLimit-Limit") != "2" || rec.Header().Get("RateLimit-Remaining") != "0" || rec.Header().Get("RateLimit-Reset") == "" {
		t.Fatalf("RateLimit headers = %v", rec.Header())
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("the 429 must carry the request id like every other error")
	}

	if other := whoamiFrom(h, "198.51.100.8:4000"); other.Code != http.StatusOK {
		t.Fatalf("another address must be unaffected: %d", other.Code)
	}
}

func TestTheClientLimitIsPerClient(t *testing.T) {
	var current Principal
	auth := authFunc(func(*http.Request) (Principal, error) { return current, nil })
	h, _ := guardedHandler(t, newTestLog(t), auth, Limits{Client: perHour(1)}, nil)

	current = Principal{ClientID: "alpha", Role: RoleDeveloper}
	if rec := whoamiFrom(h, "192.0.2.1:1"); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	if rec := whoamiFrom(h, "192.0.2.1:1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("alpha's second request must be limited: %d", rec.Code)
	}
	current = Principal{ClientID: "beta", Role: RoleDeveloper}
	if rec := whoamiFrom(h, "192.0.2.1:1"); rec.Code != http.StatusOK {
		t.Fatalf("beta has its own bucket: %d", rec.Code)
	}
}

type authFunc func(*http.Request) (Principal, error)

func (f authFunc) Authenticate(r *http.Request) (Principal, error) { return f(r) }

func TestRepeatedFailedAuthenticationIsThrottledBeforeTheKeyIsChecked(t *testing.T) {
	auth := &fakeAuth{err: ErrUnauthenticated}
	h, fake := guardedHandler(t, newTestLog(t), auth, Limits{AuthFailure: perHour(3)}, nil)

	for i := range 3 {
		if rec := whoamiFrom(h, "203.0.113.9:1"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i, rec.Code)
		}
	}
	checked := auth.calls
	rec := whoamiFrom(h, "203.0.113.9:1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after 3 failures the address must be throttled, got %d", rec.Code)
	}
	if auth.calls != checked {
		t.Fatal("a throttled address must not get to guess again: the key must not be checked")
	}

	// A different address is unaffected, and so is a valid key from a clean address.
	auth.err, auth.principal = nil, Principal{ClientID: "c", Role: RoleDeveloper}
	if rec := whoamiFrom(h, "203.0.113.10:1"); rec.Code != http.StatusOK {
		t.Fatalf("clean address: %d", rec.Code)
	}

	fake.Advance(time.Hour) // the bucket refills
	if rec := whoamiFrom(h, "203.0.113.9:1"); rec.Code != http.StatusOK {
		t.Fatalf("after the window the address is trusted again: %d", rec.Code)
	}
}

func TestSuccessfulAuthenticationsAreNotChargedAsFailures(t *testing.T) {
	auth := &fakeAuth{principal: Principal{ClientID: "c", Role: RoleDeveloper}}
	h, _ := guardedHandler(t, newTestLog(t), auth, Limits{AuthFailure: perHour(1)}, nil)
	for i := range 5 {
		if rec := whoamiFrom(h, "203.0.113.20:1"); rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
}

func TestOutagesAreNotChargedAsAuthenticationFailures(t *testing.T) {
	auth := &fakeAuth{err: sharederrors.Unavailable("AUTH_UNAVAILABLE", "down")}
	h, _ := guardedHandler(t, newTestLog(t), auth, Limits{AuthFailure: perHour(1)}, nil)
	for i := range 3 {
		if rec := whoamiFrom(h, "203.0.113.30:1"); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %d: %d, a store outage must stay a 503 and never throttle clients", i, rec.Code)
		}
	}
}

func TestPublicRoutesAreNeverRateLimited(t *testing.T) {
	h, _ := guardedHandler(t, newTestLog(t), nil, Limits{IP: perHour(1), AuthFailure: perHour(1)}, nil)
	for i := range 5 {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("probe %d: %d; health checks must never be limited", i, rec.Code)
		}
	}
}

func TestLimiterFailureLetsTheRequestThroughAndIsLogged(t *testing.T) {
	tl := newTestLog(t)
	auth := &fakeAuth{principal: Principal{ClientID: "c", Role: RoleDeveloper}}
	h := NewPublicHandler(PublicDeps{
		Logger: tl.Logger, Auth: auth, Health: NewHealth(time.Second), MaxBodyBytes: 1 << 10,
		Limiter: failingLimiter{}, Limits: Limits{IP: perHour(1), Client: perHour(1), AuthFailure: perHour(1)},
	})
	if rec := whoamiFrom(h, "192.0.2.1:1"); rec.Code != http.StatusOK {
		t.Fatalf("a limiter bug must not take the API down: %d", rec.Code)
	}
	if !strings.Contains(tl.Raw(), "rate limiter failed") {
		t.Fatalf("the failure must be logged:\n%s", tl.Raw())
	}
}

func TestAuthorisationDenialIsAudited(t *testing.T) {
	auditor := &recordingAuditor{}
	auth := &fakeAuth{principal: Principal{ClientID: "c9", Role: Role("NOBODY")}} // a role with no permissions
	h, _ := guardedHandler(t, newTestLog(t), auth, Limits{}, auditor)

	rec := whoamiFrom(h, "192.0.2.1:1")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
	if len(auditor.events) != 1 {
		t.Fatalf("audit events = %+v", auditor.events)
	}
	e := auditor.events[0]
	if e.Action != ActionAuthzDenied || e.ClientID != "c9" || e.Resource != "GET /v1/whoami" || e.Outcome != "denied" || e.RequestID == "" {
		t.Fatalf("event = %+v", e)
	}
}

func TestAuditFailureDoesNotChangeTheResponse(t *testing.T) {
	tl := newTestLog(t)
	auditor := &recordingAuditor{err: errors.New("audit table unreachable")}
	auth := &fakeAuth{principal: Principal{ClientID: "c", Role: Role("NOBODY")}}
	h, _ := guardedHandler(t, tl, auth, Limits{}, auditor)
	if rec := whoamiFrom(h, "192.0.2.1:1"); rec.Code != http.StatusForbidden {
		t.Fatalf("still 403: %d", rec.Code)
	}
	if !strings.Contains(tl.Raw(), "audit write failed") {
		t.Fatalf("the failure must be logged:\n%s", tl.Raw())
	}
}

func TestAuthenticationFailuresAreNotAudited(t *testing.T) {
	auditor := &recordingAuditor{}
	h, _ := guardedHandler(t, newTestLog(t), &fakeAuth{err: ErrUnauthenticated}, Limits{}, auditor)
	whoamiFrom(h, "192.0.2.1:1")
	if len(auditor.events) != 0 {
		t.Fatalf("unauthenticated callers must not be able to write audit rows: %+v", auditor.events)
	}
}
