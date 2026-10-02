package apiauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFailureObserverReceivesOnlyTheFailureClass(t *testing.T) {
	past := t0.Add(-time.Hour)
	cases := map[string]struct {
		mutate func(f *fixture) string
		class  string
	}{
		"no header":       {func(f *fixture) string { return "" }, ClassMissing},
		"malformed":       {func(f *fixture) string { return "nonsense" }, ClassMalformed},
		"unknown":         {func(f *fixture) string { f.store.found = false; return f.token }, ClassInvalidCredentials},
		"revoked":         {func(f *fixture) string { f.store.rec.RevokedAt = &past; return f.token }, ClassInactive},
		"expired":         {func(f *fixture) string { f.store.rec.ExpiresAt = &past; return f.token }, ClassInactive},
		"inactive client": {func(f *fixture) string { f.store.rec.ClientActive = false; return f.token }, ClassInactive},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			var got []string
			f.auth.WithFailureObserver(func(_ context.Context, class string) { got = append(got, class) })
			_, _ = f.auth.Authenticate(f.request(c.mutate(f)))
			if len(got) != 1 || got[0] != c.class {
				t.Fatalf("observed %v, want exactly [%s]", got, c.class)
			}
		})
	}
}

func TestSuccessAndOutagesAreNotReportedAsAuthenticationFailures(t *testing.T) {
	f := newFixture(t)
	called := false
	f.auth.WithFailureObserver(func(context.Context, string) { called = true })
	if _, err := f.auth.Authenticate(f.request(f.token)); err != nil || called {
		t.Fatalf("a successful authentication must not be a failure (err=%v called=%v)", err, called)
	}
	f.store.findErr = errors.New("down")
	if _, err := f.auth.Authenticate(f.request(f.token)); err == nil || called {
		t.Fatalf("a store outage is not an authentication failure (called=%v)", called)
	}
}
