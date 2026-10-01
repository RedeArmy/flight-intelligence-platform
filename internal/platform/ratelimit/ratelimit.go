// Package ratelimit implements token-bucket rate limiting with a Redis adapter, an in-memory adapter and a fallback
// that keeps the service protected when Redis is down (ADR-004, ADR-032). It knows nothing about HTTP.
package ratelimit

import (
	"context"
	"time"
)

// Rule is a token bucket: it holds Limit tokens and refills them evenly over Window, so Limit is both the burst
// size and the average number of requests allowed per Window.
type Rule struct {
	Limit  int
	Window time.Duration
}

// Valid reports whether the rule can be enforced.
func (r Rule) Valid() bool { return r.Limit >= 1 && r.Window > 0 }

// Decision is the outcome of one Allow call.
type Decision struct {
	Allowed bool
	// Limit is the bucket size of the rule that was applied.
	Limit int
	// Remaining is the whole number of tokens left after the call.
	Remaining int
	// RetryAfter is how long to wait before the call could succeed; zero when it was allowed.
	RetryAfter time.Duration
	// ResetAfter is how long until the bucket is full again.
	ResetAfter time.Duration
}

// Limiter takes tokens from the bucket named by key.
//
// cost is the number of tokens to take. A cost of zero takes nothing and reports whether at least one token is
// available, which lets a caller check a limit before doing work and charge it only if the work fails.
type Limiter interface {
	Allow(ctx context.Context, key string, rule Rule, cost int) (Decision, error)
}

// ceilSeconds rounds a duration up to whole seconds, for Retry-After style headers.
func ceilSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// RetryAfterSeconds is the Retry-After header value for a denied decision: at least one second.
func (d Decision) RetryAfterSeconds() int { return max(1, ceilSeconds(d.RetryAfter)) }

// ResetSeconds is the RateLimit-Reset header value.
func (d Decision) ResetSeconds() int { return ceilSeconds(d.ResetAfter) }
