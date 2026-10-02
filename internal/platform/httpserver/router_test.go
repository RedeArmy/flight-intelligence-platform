package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver/openapi"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

var errBoom = errors.New("backend exploded")

type fakeAuth struct {
	principal Principal
	err       error
	calls     int
}

func (f *fakeAuth) Authenticate(*http.Request) (Principal, error) {
	f.calls++
	return f.principal, f.err
}

func publicHandler(tl *testLog, auth Authenticator, health *Health) http.Handler {
	if health == nil {
		health = NewHealth(time.Second)
	}
	return NewPublicHandler(PublicDeps{Logger: tl.Logger, Auth: auth, Health: health, MaxBodyBytes: 1 << 10})
}

func TestWhoamiReturnsTheAuthenticatedPrincipal(t *testing.T) {
	auth := &fakeAuth{principal: Principal{ClientID: "client-1", Role: RoleDeveloper}}
	rec := doReq(publicHandler(newTestLog(t), auth, nil), http.MethodGet, "/v1/whoami",
		withHeader("Authorization", "Bearer whatever"), withHeader("X-Request-Id", "req-whoami-001"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got openapi.Whoami
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ClientId != "client-1" || got.Role != openapi.WhoamiRole("DEVELOPER") {
		t.Errorf("body = %+v", got)
	}
	if rec.Header().Get("X-Request-Id") != "req-whoami-001" {
		t.Errorf("request id header = %q", rec.Header().Get("X-Request-Id"))
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("content type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestProtectedRoutesAreClosedByDefault(t *testing.T) {
	rec := doReq(publicHandler(newTestLog(t), nil, nil), http.MethodGet, "/v1/whoami",
		withHeader("Authorization", "Bearer fip_abcd1234_xxxxxxxxxxxxxxxxxxxxxxxx"))
	if rec.Code != http.StatusUnauthorized || decodeError(t, rec).Code != sharederrors.CodeUnauthenticated {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("WWW-Authenticate = %q", rec.Header().Get("WWW-Authenticate"))
	}
}

func TestAuthenticationFailuresAreMappedByKind(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		status     int
		wantBearer bool
	}{
		{"unauthenticated", ErrUnauthenticated, http.StatusUnauthorized, true},
		{"store unavailable is not a 401", sharederrors.Unavailable("KEY_STORE_DOWN", "try later"), http.StatusServiceUnavailable, false},
		{"a deadline is a 504", context.DeadlineExceeded, http.StatusGatewayTimeout, false},
		{"an unclassified failure is a 500", errBoom, http.StatusInternalServerError, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doReq(publicHandler(newTestLog(t), &fakeAuth{err: tc.err}, nil), http.MethodGet, "/v1/whoami")
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if got := rec.Header().Get("WWW-Authenticate") != ""; got != tc.wantBearer {
				t.Errorf("WWW-Authenticate present = %v, want %v", got, tc.wantBearer)
			}
		})
	}
}

func TestRoleWithoutPermissionIsForbidden(t *testing.T) {
	auth := &fakeAuth{principal: Principal{ClientID: "c", Role: Role("GUEST")}}
	rec := doReq(publicHandler(newTestLog(t), auth, nil), http.MethodGet, "/v1/whoami")
	if rec.Code != http.StatusForbidden || decodeError(t, rec).Code != sharederrors.CodeForbidden {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	empty := &fakeAuth{principal: Principal{ClientID: "c"}}
	if rec := doReq(publicHandler(newTestLog(t), empty, nil), http.MethodGet, "/v1/whoami"); rec.Code != http.StatusForbidden {
		t.Fatalf("a principal with no role must be forbidden, got %d", rec.Code)
	}
}

func TestEveryRoleMayCallWhoami(t *testing.T) {
	for _, role := range []Role{RoleUser, RoleDeveloper, RoleOperator, RoleAdmin, RoleService} {
		auth := &fakeAuth{principal: Principal{ClientID: "c", Role: role}}
		if rec := doReq(publicHandler(newTestLog(t), auth, nil), http.MethodGet, "/v1/whoami"); rec.Code != http.StatusOK {
			t.Errorf("role %s: status %d", role, rec.Code)
		}
	}
}

func TestProbesAreOpenAndDoNotAuthenticate(t *testing.T) {
	auth := &fakeAuth{err: ErrUnauthenticated}
	h := publicHandler(newTestLog(t), auth, nil)

	live := doReq(h, http.MethodGet, "/healthz")
	if live.Code != http.StatusOK || strings.TrimSpace(live.Body.String()) != `{"status":"ok"}` {
		t.Errorf("healthz: %d %s", live.Code, live.Body.String())
	}
	ready := doReq(h, http.MethodGet, "/readyz")
	if ready.Code != http.StatusOK || strings.TrimSpace(ready.Body.String()) != `{"status":"ready"}` {
		t.Errorf("readyz: %d %s", ready.Code, ready.Body.String())
	}
	if auth.calls != 0 {
		t.Errorf("probes must not call the authenticator (%d calls)", auth.calls)
	}
}

func TestReadyzReflectsHealth(t *testing.T) {
	health := NewHealth(time.Second, failCheck("db", true), okCheck("cache", false))
	rec := doReq(publicHandler(newTestLog(t), nil, health), http.MethodGet, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	var got openapi.Readiness
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "not_ready" || got.Checks == nil || (*got.Checks)["db"] != "failed" || (*got.Checks)["cache"] != "ok" {
		t.Errorf("body = %s", rec.Body.String())
	}

	degraded := NewHealth(time.Second, okCheck("db", true), failCheck("cache", false))
	if rec := doReq(publicHandler(newTestLog(t), nil, degraded), http.MethodGet, "/readyz"); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"degraded"`) {
		t.Errorf("degraded readiness: %d %s", rec.Code, rec.Body.String())
	}

	draining := NewHealth(time.Second)
	draining.SetDraining()
	if rec := doReq(publicHandler(newTestLog(t), nil, draining), http.MethodGet, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("draining readiness: %d", rec.Code)
	}
}

func TestUnknownRoutesAndMethodsOnThePublicAPI(t *testing.T) {
	h := publicHandler(newTestLog(t), nil, nil)
	if rec := doReq(h, http.MethodGet, "/v1/nope"); rec.Code != http.StatusNotFound || decodeError(t, rec).Code != sharederrors.CodeNotFound {
		t.Errorf("404: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doReq(h, http.MethodPost, "/v1/whoami"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("405: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCredentialsNeverReachTheLogs(t *testing.T) {
	tl := newTestLog(t)
	const key = "fip_abcd1234_zzzzzzzzzzzzzzzzzzzzzzzz"
	auth := &fakeAuth{principal: Principal{ClientID: "client-1", Role: RoleAdmin}}
	doReq(publicHandler(tl, auth, nil), http.MethodGet, "/v1/whoami", withHeader("Authorization", "Bearer "+key))
	doReq(publicHandler(tl, nil, nil), http.MethodGet, "/v1/whoami", withHeader("Authorization", "Bearer "+key))
	if strings.Contains(tl.Raw(), key) || strings.Contains(tl.Raw(), "zzzzzzzzzz") {
		t.Fatalf("an API key reached the logs:\n%s", tl.Raw())
	}
}

func TestRoutesWithoutAPolicyAreRefused(t *testing.T) {
	tl := newTestLog(t)
	r := newBaseRouter(tl.Logger, nil)
	called := false
	r.With(enforcePolicy(guard{auth: &fakeAuth{principal: Principal{ClientID: "c", Role: RoleAdmin}}})).
		Get("/v1/unlisted", func(http.ResponseWriter, *http.Request) { called = true })

	rec := doReq(r, http.MethodGet, "/v1/unlisted")
	if rec.Code != http.StatusInternalServerError || decodeError(t, rec).Code != CodeRoutePolicyMissing {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("handler ran although the route has no policy")
	}
	if tl.Last("request failed") == nil {
		t.Error("a missing policy is a programming error and must be logged")
	}
}

func TestOperatorHandlerServesOnlyItsOwnRoutes(t *testing.T) {
	tl := newTestLog(t)
	op := NewOperatorHandler(OperatorDeps{Logger: tl.Logger, Routes: func(r chi.Router) {
		r.Get("/metrics", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("# metrics")) })
	}})
	pub := publicHandler(tl, nil, nil)

	if rec := doReq(op, http.MethodGet, "/metrics"); rec.Code != http.StatusOK {
		t.Errorf("operator /metrics: %d", rec.Code)
	}
	for _, path := range []string{"/healthz", "/readyz", "/v1/whoami"} {
		if rec := doReq(op, http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("operator listener must not serve public route %s, got %d", path, rec.Code)
		}
	}
	if rec := doReq(pub, http.MethodGet, "/metrics"); rec.Code != http.StatusNotFound {
		t.Errorf("public listener must not serve /metrics, got %d", rec.Code)
	}
	if rec := doReq(NewOperatorHandler(OperatorDeps{Logger: tl.Logger}), http.MethodGet, "/anything"); rec.Code != http.StatusNotFound {
		t.Errorf("operator handler without routes: %d", rec.Code)
	}
}

func TestBadRequestHandlerHidesParserDetail(t *testing.T) {
	tl := newTestLog(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	badRequest(rec, req.WithContext(withLogger(req.Context(), tl.Logger)), context.Canceled)
	if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != sharederrors.CodeInvalidRequest ||
		strings.Contains(rec.Body.String(), "canceled") {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]struct {
		token string
		ok    bool
	}{
		"Bearer abc123":     {"abc123", true},
		"bearer abc123":     {"abc123", true},
		"  Bearer   abc123": {"abc123", true},
		"Basic abc123":      {"", false},
		"Bearer":            {"", false},
		"Bearer ":           {"", false},
		"":                  {"", false},
		"abc123":            {"", false},
	}
	for header, want := range cases {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.Header.Set("Authorization", header)
		token, ok := BearerToken(req)
		if token != want.token || ok != want.ok {
			t.Errorf("BearerToken(%q) = %q, %v; want %q, %v", header, token, ok, want.token, want.ok)
		}
	}
}

func TestWhoamiWithoutAPrincipalIsAnInternalWiringError(t *testing.T) {
	_, err := (&api{health: NewHealth(time.Second)}).GetWhoami(context.Background(), openapi.GetWhoamiRequestObject{})
	if sharederrors.KindOf(err) != sharederrors.KindInternal {
		t.Fatalf("err = %v", err)
	}
}

func TestDenyAllAlwaysRejects(t *testing.T) {
	_, err := DenyAll{}.Authenticate(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
	if err == nil || sharederrors.KindOf(err) != sharederrors.KindUnauthenticated {
		t.Fatalf("err = %v", err)
	}
}
