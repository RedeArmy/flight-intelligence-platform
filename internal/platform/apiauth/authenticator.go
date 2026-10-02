// Package apiauth authenticates API keys against PostgreSQL (ADR-016, ADR-027). It implements the
// httpserver.Authenticator port; HTTP routing and the permission table stay in httpserver.
package apiauth

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/httpserver"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/security"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
	sharederrors "github.com/RedeArmy/flight-intelligence-platform/internal/shared/errors"
)

// CodeAuthUnavailable marks a failure to check credentials because the key store is down. It is not a 401: the
// caller is not wrongly authenticated, the platform cannot decide.
const CodeAuthUnavailable = "AUTH_UNAVAILABLE"

// lastUsedGranularity limits how often a key's last_used_at is rewritten, so a busy key does not turn every
// request into a database write.
const lastUsedGranularity = 5 * time.Minute

// KeyRecord is what the authenticator needs to know about a key and its client.
type KeyRecord struct {
	KeyID        string
	ClientID     string
	Role         string
	ClientActive bool
	Hash         []byte
	ExpiresAt    *time.Time
	RevokedAt    *time.Time
	LastUsedAt   *time.Time
}

// KeyStore looks keys up. FindByPrefix reports found=false for an unknown prefix.
type KeyStore interface {
	FindByPrefix(ctx context.Context, prefix string) (rec KeyRecord, found bool, err error)
	TouchLastUsed(ctx context.Context, keyID string, at time.Time) error
}

// Authenticator resolves the bearer API key of a request to a principal.
type Authenticator struct {
	store     KeyStore
	hasher    *security.KeyHasher
	clock     clock.Nower
	logger    *slog.Logger
	onFailure func(ctx context.Context, reasonClass string)
	// decoy is compared against when the prefix is unknown, so unknown and wrong keys cost the same (SR-23).
	decoy []byte
}

var _ httpserver.Authenticator = (*Authenticator)(nil)

// New returns an Authenticator.
func New(store KeyStore, hasher *security.KeyHasher, now clock.Nower, logger *slog.Logger) *Authenticator {
	return &Authenticator{store: store, hasher: hasher, clock: now, logger: logger, decoy: hasher.Hash("decoy")}
}

// Failure classes reported to the observer: a short fixed vocabulary, safe as a metric label.
const (
	ClassMissing            = "missing"
	ClassMalformed          = "malformed"
	ClassInvalidCredentials = "invalid_credentials" // #nosec G101 -- a metric label, not a credential
	ClassInactive           = "inactive"            // revoked, expired or owned by a disabled client
)

// WithFailureObserver registers a function called for every failed authentication with its failure class, for
// metrics. It never receives the reason text or any key material.
func (a *Authenticator) WithFailureObserver(fn func(ctx context.Context, reasonClass string)) *Authenticator {
	a.onFailure = fn
	return a
}

func (a *Authenticator) observe(ctx context.Context, class string) {
	if a.onFailure != nil {
		a.onFailure(ctx, class)
	}
}

// Authenticate implements httpserver.Authenticator. Every credential problem (missing, malformed, unknown,
// wrong, expired, revoked, inactive client) returns the same ErrUnauthenticated; the reason goes to the log only.
func (a *Authenticator) Authenticate(r *http.Request) (httpserver.Principal, error) {
	ctx := r.Context()
	token, ok := httpserver.BearerToken(r)
	if !ok {
		a.observe(ctx, ClassMissing)
		return httpserver.Principal{}, httpserver.ErrUnauthenticated
	}
	prefix, keySecret, err := security.ParseKey(token)
	if err != nil {
		a.reject(ctx, "malformed", "", ClassMalformed)
		return httpserver.Principal{}, httpserver.ErrUnauthenticated
	}

	rec, found, err := a.store.FindByPrefix(ctx, prefix)
	if err != nil {
		a.logger.ErrorContext(ctx, "api key lookup failed", "error", err)
		return httpserver.Principal{}, sharederrors.Unavailable(CodeAuthUnavailable, "authentication is temporarily unavailable").WithCause(err)
	}
	stored := a.decoy
	if found {
		stored = rec.Hash
	}
	secretMatches := a.hasher.Matches(stored, keySecret)
	if !found || !secretMatches {
		a.reject(ctx, "unknown_or_wrong", prefix, ClassInvalidCredentials)
		return httpserver.Principal{}, httpserver.ErrUnauthenticated
	}
	now := a.clock.Now()
	if reason := inactiveReason(rec, now); reason != "" {
		a.reject(ctx, reason, prefix, ClassInactive)
		return httpserver.Principal{}, httpserver.ErrUnauthenticated
	}

	a.touch(ctx, rec, now)
	return httpserver.Principal{ClientID: rec.ClientID, Role: httpserver.Role(rec.Role)}, nil
}

func inactiveReason(rec KeyRecord, now time.Time) string {
	switch {
	case rec.RevokedAt != nil:
		return "revoked"
	case rec.ExpiresAt != nil && !now.Before(*rec.ExpiresAt):
		return "expired"
	case !rec.ClientActive:
		return "client_inactive"
	}
	return ""
}

// reject records why authentication failed. The prefix is a public identifier, never the secret.
func (a *Authenticator) reject(ctx context.Context, reason, prefix, class string) {
	a.observe(ctx, class)
	a.logger.WarnContext(ctx, "authentication failed", "reason", reason, "key_prefix", prefix)
}

// touch refreshes last_used_at at most once per lastUsedGranularity. It is best effort: a failure never fails the request.
func (a *Authenticator) touch(ctx context.Context, rec KeyRecord, now time.Time) {
	if rec.LastUsedAt != nil && now.Sub(*rec.LastUsedAt) < lastUsedGranularity {
		return
	}
	if err := a.store.TouchLastUsed(ctx, rec.KeyID, now); err != nil {
		a.logger.WarnContext(ctx, "could not update last_used_at", "error", err)
	}
}
