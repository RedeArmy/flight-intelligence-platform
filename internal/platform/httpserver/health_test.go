package httpserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func okCheck(name string, critical bool) Check {
	return Check{Name: name, Critical: critical, Run: func(context.Context) error { return nil }}
}

func failCheck(name string, critical bool) Check {
	return Check{Name: name, Critical: critical, Run: func(context.Context) error { return errors.New("down") }}
}

func TestReadinessStates(t *testing.T) {
	cases := []struct {
		name   string
		checks []Check
		drain  bool
		want   string
	}{
		{"no checks is ready", nil, false, StateReady},
		{"all ok", []Check{okCheck("db", true), okCheck("cache", false)}, false, StateReady},
		{"critical failure", []Check{failCheck("db", true), okCheck("cache", false)}, false, StateNotReady},
		{"optional failure degrades", []Check{okCheck("db", true), failCheck("cache", false)}, false, StateDegraded},
		{"critical beats optional", []Check{failCheck("db", true), failCheck("cache", false)}, false, StateNotReady},
		{"draining overrides healthy checks", []Check{okCheck("db", true)}, true, StateNotReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHealth(time.Second, tc.checks...)
			if tc.drain {
				h.SetDraining()
			}
			state, results := h.Readiness(context.Background())
			if state != tc.want {
				t.Fatalf("state = %q, want %q (results %v)", state, tc.want, results)
			}
			if len(results) != len(tc.checks) {
				t.Errorf("results = %v", results)
			}
		})
	}
}

func TestReadinessReportsPerCheckResult(t *testing.T) {
	h := NewHealth(time.Second, okCheck("db", true), failCheck("cache", false))
	_, results := h.Readiness(context.Background())
	if results["db"] != "ok" || results["cache"] != "failed" {
		t.Fatalf("results = %v", results)
	}
}

func TestReadinessTimesOutSlowChecks(t *testing.T) {
	slow := Check{Name: "slow", Critical: true, Run: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	h := NewHealth(20*time.Millisecond, slow)
	done := make(chan string, 1)
	go func() { s, _ := h.Readiness(context.Background()); done <- s }()
	select {
	case state := <-done:
		if state != StateNotReady {
			t.Fatalf("state = %q", state)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readiness did not honour the check timeout")
	}
}

func TestReadinessTreatsAPanicAsFailure(t *testing.T) {
	boom := Check{Name: "boom", Critical: true, Run: func(context.Context) error { panic("check exploded") }}
	state, results := NewHealth(time.Second, boom).Readiness(context.Background())
	if state != StateNotReady || results["boom"] != "failed" {
		t.Fatalf("state = %q, results = %v", state, results)
	}
}

func TestReadinessRunsChecksConcurrently(t *testing.T) {
	// Each check waits until both have started. Run sequentially, the first would block until its timeout and fail.
	var barrier sync.WaitGroup
	barrier.Add(2)
	rendezvous := func(name string) Check {
		return Check{Name: name, Critical: true, Run: func(context.Context) error {
			barrier.Done()
			barrier.Wait()
			return nil
		}}
	}
	state, _ := NewHealth(2*time.Second, rendezvous("a"), rendezvous("b")).Readiness(context.Background())
	if state != StateReady {
		t.Fatalf("checks did not run concurrently: state = %q", state)
	}
}

func TestDrainingFlag(t *testing.T) {
	h := NewHealth(time.Second)
	if h.Draining() {
		t.Fatal("must not start draining")
	}
	h.SetDraining()
	if !h.Draining() {
		t.Fatal("SetDraining had no effect")
	}
}
