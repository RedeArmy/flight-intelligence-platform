package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
)

// DefaultMaxKeys bounds the memory of a Memory limiter: a flood of distinct keys (for example spoofed addresses)
// cannot grow it without limit.
const DefaultMaxKeys = 100_000

// sweepEvery is how many calls pass between sweeps of buckets that have refilled completely.
const sweepEvery = 1024

type bucket struct {
	tokens  float64
	updated time.Time
	window  time.Duration // kept so a sweep knows when the bucket is full again
	limit   float64
}

// Memory is a per-process token-bucket limiter. It is the fallback when Redis is unavailable and the only limiter
// when Redis is not configured; its limits apply to one instance, not to the fleet.
type Memory struct {
	clock   clock.Nower
	maxKeys int

	mu      sync.Mutex
	buckets map[string]*bucket
	calls   int
}

var _ Limiter = (*Memory)(nil)

// NewMemory returns a Memory limiter holding at most maxKeys buckets (DefaultMaxKeys if maxKeys < 1).
func NewMemory(now clock.Nower, maxKeys int) *Memory {
	if maxKeys < 1 {
		maxKeys = DefaultMaxKeys
	}
	return &Memory{clock: now, maxKeys: maxKeys, buckets: make(map[string]*bucket)}
}

// Allow implements Limiter.
func (m *Memory) Allow(ctx context.Context, key string, rule Rule, cost int) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	now := m.clock.Now()
	limit := float64(rule.Limit)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.maintain(now)

	b, ok := m.buckets[key]
	if !ok {
		b = &bucket{tokens: limit, updated: now}
		m.buckets[key] = b
	}
	b.window, b.limit = rule.Window, limit
	b.tokens = math.Min(limit, b.tokens+float64(now.Sub(b.updated))*limit/float64(rule.Window))
	b.updated = now

	need := float64(max(cost, 1))
	d := Decision{Limit: rule.Limit}
	if b.tokens >= need {
		d.Allowed = true
		b.tokens -= float64(cost)
	} else {
		d.RetryAfter = time.Duration(math.Ceil((need - b.tokens) * float64(rule.Window) / limit))
	}
	d.Remaining = int(b.tokens)
	d.ResetAfter = time.Duration(math.Ceil((limit - b.tokens) * float64(rule.Window) / limit))
	return d, nil
}

// maintain drops buckets that have refilled completely (they behave like a new one) and keeps the map bounded.
// The caller holds m.mu.
func (m *Memory) maintain(now time.Time) {
	m.calls++
	if m.calls%sweepEvery == 0 {
		for k, b := range m.buckets {
			if full(b, now) {
				delete(m.buckets, k)
			}
		}
	}
	for len(m.buckets) >= m.maxKeys {
		for k := range m.buckets { // map order is random: evicts an arbitrary bucket
			delete(m.buckets, k)
			break
		}
	}
}

func full(b *bucket, now time.Time) bool {
	return b.tokens+float64(now.Sub(b.updated))*b.limit/float64(b.window) >= b.limit
}

// Len returns the number of buckets held (for tests and metrics).
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.buckets)
}
