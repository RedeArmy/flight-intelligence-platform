package httpserver

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Check is one readiness check. A failing Critical check makes the service not ready; a failing non-critical
// check only marks it degraded (for example Redis, ADR-004).
type Check struct {
	Name     string
	Critical bool
	Run      func(ctx context.Context) error
}

// Readiness states.
const (
	StateReady    = "ready"
	StateDegraded = "degraded"
	StateNotReady = "not_ready"
)

const (
	checkOK     = "ok"
	checkFailed = "failed"
)

// DefaultReadinessCacheTTL is how long a readiness result is reused. /readyz is public, so without it every
// anonymous request would reach PostgreSQL and Redis: a flood of probes would become backend load. A second is short
// enough for an orchestrator that probes every few seconds to see a failure promptly.
const DefaultReadinessCacheTTL = time.Second

// Health holds the readiness checks and the draining flag.
type Health struct {
	checks   []Check
	timeout  time.Duration
	draining atomic.Bool
	now      func() time.Time

	mu       sync.Mutex // held while the checks run, so concurrent callers share one evaluation
	cacheTTL time.Duration
	cached   cachedReadiness
}

type cachedReadiness struct {
	at      time.Time
	valid   bool
	state   string
	results map[string]string
}

// NewHealth builds a Health. Each check runs concurrently under its own timeout, and the result is reused for
// DefaultReadinessCacheTTL.
func NewHealth(timeout time.Duration, checks ...Check) *Health {
	return &Health{checks: checks, timeout: timeout, now: time.Now, cacheTTL: DefaultReadinessCacheTTL}
}

// SetCacheTTL changes how long a result is reused; zero or less evaluates the checks on every call. Call it before
// serving.
func (h *Health) SetCacheTTL(ttl time.Duration) { h.cacheTTL = ttl }

// SetDraining marks the service as shutting down: readiness reports not_ready from now on.
func (h *Health) SetDraining() { h.draining.Store(true) }

// Draining reports whether SetDraining was called.
func (h *Health) Draining() bool { return h.draining.Load() }

// Readiness returns the overall state and per-check results. The checks run at most once per cache TTL however many
// callers ask; callers that arrive while they run wait for that evaluation instead of starting another. Draining is
// applied on every call, so a shutdown is visible at once.
func (h *Health) Readiness(ctx context.Context) (state string, results map[string]string) {
	state, cached := h.evaluate(ctx)
	results = make(map[string]string, len(cached))
	for name, r := range cached {
		results[name] = r
	}
	if h.Draining() {
		state = StateNotReady
	}
	return state, results
}

// evaluate returns the cached result while it is fresh and otherwise runs the checks. The checks run detached from
// the caller's context: the result is shared, so one caller going away must not fail it for the rest.
func (h *Health) evaluate(ctx context.Context) (string, map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cached.valid && h.now().Sub(h.cached.at) < h.cacheTTL {
		return h.cached.state, h.cached.results
	}
	failures := h.runChecks(context.WithoutCancel(ctx))
	state := StateReady
	results := make(map[string]string, len(h.checks))
	for _, c := range h.checks {
		if failures[c.Name] {
			results[c.Name] = checkFailed
			state = worse(state, c.Critical)
		} else {
			results[c.Name] = checkOK
		}
	}
	h.cached = cachedReadiness{at: h.now(), valid: true, state: state, results: results}
	return state, results
}

func worse(current string, critical bool) string {
	if critical {
		return StateNotReady
	}
	if current == StateReady {
		return StateDegraded
	}
	return current
}

// runChecks runs all checks in parallel and returns the set of failed check names.
func (h *Health) runChecks(ctx context.Context) map[string]bool {
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		failed = map[string]bool{}
	)
	for _, c := range h.checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.runOne(ctx, c); err != nil {
				mu.Lock()
				failed[c.Name] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return failed
}

// runOne runs a check with a timeout and treats a panic as a failure.
func (h *Health) runOne(ctx context.Context, c Check) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("check %s panicked: %v", c.Name, rec)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	return c.Run(ctx)
}
