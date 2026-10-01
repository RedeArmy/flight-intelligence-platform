package apiauth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/security"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

type fakeStore struct {
	rec      KeyRecord
	found    bool
	findErr  error
	touchErr error
	touched  []time.Time
	looked   []string
}

func (f *fakeStore) FindByPrefix(_ context.Context, prefix string) (KeyRecord, bool, error) {
	f.looked = append(f.looked, prefix)
	return f.rec, f.found, f.findErr
}

func (f *fakeStore) TouchLastUsed(_ context.Context, _ string, at time.Time) error {
	f.touched = append(f.touched, at)
	return f.touchErr
}

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type fixture struct {
	auth  *Authenticator
	store *fakeStore
	clock *clock.Fake
	token string
	logs  *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	hasher, err := security.NewKeyHasher(secret.Secret(strings.Repeat("pepper-", 6)))
	if err != nil {
		t.Fatal(err)
	}
	key, err := hasher.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{found: true, rec: KeyRecord{
		KeyID: "key-1", ClientID: "client-1", Role: "DEVELOPER", ClientActive: true, Hash: key.Hash,
	}}
	logs := &bytes.Buffer{}
	fake := clock.NewFake(t0)
	return &fixture{
		auth:  New(store, hasher, fake, slog.New(slog.NewTextHandler(logs, nil))),
		store: store, clock: fake, token: key.Token.Reveal(), logs: logs,
	}
}

func (f *fixture) request(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestAuthenticateAcceptsAValidKey(t *testing.T) {
	f := newFixture(t)
	p, err := f.auth.Authenticate(f.request(f.token))
	if err != nil {
		t.Fatal(err)
	}
	if p.ClientID != "client-1" || p.Role != httpserver.RoleDeveloper {
		t.Fatalf("principal = %+v", p)
	}
}

func TestEveryCredentialProblemIsTheSameUnauthenticatedError(t *testing.T) {
	past, future := t0.Add(-time.Hour), t0.Add(time.Hour)
	cases := map[string]func(f *fixture) string{
		"no header":      func(f *fixture) string { return "" },
		"malformed":      func(f *fixture) string { return "nonsense" },
		"unknown prefix": func(f *fixture) string { f.store.found = false; return f.token },
		"wrong secret": func(f *fixture) string {
			return f.token[:len(f.token)-1] + map[bool]string{true: "B", false: "A"}[strings.HasSuffix(f.token, "A")]
		},
		"revoked":         func(f *fixture) string { f.store.rec.RevokedAt = &past; return f.token },
		"expired":         func(f *fixture) string { f.store.rec.ExpiresAt = &past; return f.token },
		"expires exactly": func(f *fixture) string { e := t0; f.store.rec.ExpiresAt = &e; return f.token },
		"inactive client": func(f *fixture) string { f.store.rec.ClientActive = false; return f.token },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.auth.Authenticate(f.request(mutate(f)))
			if err != httpserver.ErrUnauthenticated { //nolint:errorlint // identity matters: it must be the shared sentinel
				t.Fatalf("err = %v, want exactly ErrUnauthenticated", err)
			}
		})
	}
	// A key that has not expired yet still works.
	f := newFixture(t)
	f.store.rec.ExpiresAt = &future
	if _, err := f.auth.Authenticate(f.request(f.token)); err != nil {
		t.Fatalf("an unexpired key must work: %v", err)
	}
}

func TestUnknownPrefixStillRunsTheComparison(t *testing.T) {
	f := newFixture(t)
	f.store.found = false
	if _, err := f.auth.Authenticate(f.request(f.token)); err == nil {
		t.Fatal("must fail")
	}
	if len(f.store.looked) != 1 {
		t.Fatalf("the store must be asked once, got %d", len(f.store.looked))
	}
}

func TestStoreFailureIsUnavailableNotUnauthenticated(t *testing.T) {
	f := newFixture(t)
	f.store.findErr = errors.New("connection refused")
	_, err := f.auth.Authenticate(f.request(f.token))
	if sharederrors.KindOf(err) != sharederrors.KindUnavailable || sharederrors.CodeOf(err) != CodeAuthUnavailable {
		t.Fatalf("err = %v, want unavailable %s", err, CodeAuthUnavailable)
	}
}

func TestLastUsedIsTouchedAtMostOncePerGranularity(t *testing.T) {
	f := newFixture(t)
	for range 3 {
		if _, err := f.auth.Authenticate(f.request(f.token)); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.store.touched) != 3 {
		t.Fatalf("a key never used must be touched: %d", len(f.store.touched))
	}

	f.store.touched = nil
	recent := t0.Add(-time.Minute)
	f.store.rec.LastUsedAt = &recent
	if _, err := f.auth.Authenticate(f.request(f.token)); err != nil || len(f.store.touched) != 0 {
		t.Fatalf("a key used a minute ago must not be rewritten: err=%v touched=%d", err, len(f.store.touched))
	}

	old := t0.Add(-time.Hour)
	f.store.rec.LastUsedAt = &old
	if _, err := f.auth.Authenticate(f.request(f.token)); err != nil || len(f.store.touched) != 1 {
		t.Fatalf("a stale last_used_at must be refreshed: err=%v touched=%d", err, len(f.store.touched))
	}
}

func TestTouchFailureNeverFailsTheRequest(t *testing.T) {
	f := newFixture(t)
	f.store.touchErr = errors.New("read-only replica")
	if _, err := f.auth.Authenticate(f.request(f.token)); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(f.logs.String(), "last_used_at") {
		t.Errorf("the failure should be logged:\n%s", f.logs.String())
	}
}

func TestLogsNeverContainTheSecretPart(t *testing.T) {
	f := newFixture(t)
	f.store.found = false
	_, _ = f.auth.Authenticate(f.request(f.token))
	_, keySecret, _ := security.ParseKey(f.token)
	if strings.Contains(f.logs.String(), keySecret) || strings.Contains(f.logs.String(), f.token) {
		t.Fatalf("the log must not contain key material:\n%s", f.logs.String())
	}
	if !strings.Contains(f.logs.String(), "reason=unknown_or_wrong") {
		t.Errorf("the reason must be logged:\n%s", f.logs.String())
	}
}
