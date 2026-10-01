package ratelimit

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
)

// Fallback tries the primary limiter and, when it fails, answers from a local limiter with stricter rules so the
// service stays protected while Redis is down (ADR-004). After a failure the primary is not tried again for
// retryAfter, so a dead Redis does not add its timeout to every request.
type Fallback struct {
	primary    Limiter
	local      Limiter
	clock      clock.Nower
	logger     *slog.Logger
	retryAfter time.Duration
	scale      float64

	mu        sync.Mutex
	skipUntil time.Time
}

var _ Limiter = (*Fallback)(nil)

// DefaultScale is the fraction of each limit applied per instance while Redis is down. The real number of
// instances is not known to a single process, so this is deliberately conservative.
const DefaultScale = 0.5

// NewFallback returns a Fallback. scale must be in (0, 1]; retryAfter is how long the primary is skipped after a failure.
func NewFallback(primary, local Limiter, now clock.Nower, logger *slog.Logger, scale float64, retryAfter time.Duration) *Fallback {
	if scale <= 0 || scale > 1 {
		scale = DefaultScale
	}
	return &Fallback{primary: primary, local: local, clock: now, logger: logger, scale: scale, retryAfter: retryAfter}
}

// Allow implements Limiter. It returns an error only if the local limiter does.
func (f *Fallback) Allow(ctx context.Context, key string, rule Rule, cost int) (Decision, error) {
	if f.primaryUsable() {
		d, err := f.primary.Allow(ctx, key, rule, cost)
		if err == nil {
			f.recovered(ctx)
			return d, nil
		}
		f.failed(ctx, err)
	}
	return f.local.Allow(ctx, key, f.stricter(rule), cost)
}

func (f *Fallback) primaryUsable() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.clock.Now().Before(f.skipUntil)
}

func (f *Fallback) failed(ctx context.Context, err error) {
	f.mu.Lock()
	first := f.skipUntil.IsZero()
	f.skipUntil = f.clock.Now().Add(f.retryAfter)
	f.mu.Unlock()
	if first { // log the transition, not every request
		f.logger.WarnContext(ctx, "rate limiter fell back to local limits", "error", err)
	}
}

func (f *Fallback) recovered(ctx context.Context) {
	f.mu.Lock()
	was := !f.skipUntil.IsZero()
	f.skipUntil = time.Time{}
	f.mu.Unlock()
	if was {
		f.logger.InfoContext(ctx, "rate limiter is using Redis again")
	}
}

func (f *Fallback) stricter(r Rule) Rule {
	return Rule{Limit: max(1, int(float64(r.Limit)*f.scale)), Window: r.Window}
}
