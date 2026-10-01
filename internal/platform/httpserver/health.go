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

// Health holds the readiness checks and the draining flag.
type Health struct {
	checks   []Check
	timeout  time.Duration
	draining atomic.Bool
}

// NewHealth builds a Health. Each check runs concurrently under its own timeout.
func NewHealth(timeout time.Duration, checks ...Check) *Health {
	return &Health{checks: checks, timeout: timeout}
}

// SetDraining marks the service as shutting down: readiness reports not_ready from now on.
func (h *Health) SetDraining() { h.draining.Store(true) }

// Draining reports whether SetDraining was called.
func (h *Health) Draining() bool { return h.draining.Load() }

// Readiness evaluates every check and returns the overall state and per-check results.
func (h *Health) Readiness(ctx context.Context) (state string, results map[string]string) {
	results = make(map[string]string, len(h.checks))
	failures := h.runChecks(ctx)

	state = StateReady
	for _, c := range h.checks {
		if failures[c.Name] {
			results[c.Name] = checkFailed
			state = worse(state, c.Critical)
		} else {
			results[c.Name] = checkOK
		}
	}
	if h.Draining() {
		state = StateNotReady
	}
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
