package ratelimit

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

var perMinute = Rule{Limit: 6, Window: time.Minute} // one token every ten seconds

func allow(t *testing.T, l Limiter, key string, cost int) Decision {
	t.Helper()
	d, err := l.Allow(context.Background(), key, perMinute, cost)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMemoryAllowsABurstThenDenies(t *testing.T) {
	m := NewMemory(clock.NewFake(t0), 0)
	for i := range 6 {
		d := allow(t, m, "k", 1)
		if !d.Allowed || d.Remaining != 5-i || d.Limit != 6 || d.RetryAfter != 0 {
			t.Fatalf("call %d: %+v", i, d)
		}
	}
	d := allow(t, m, "k", 1)
	if d.Allowed || d.Remaining != 0 || d.RetryAfter != 10*time.Second || d.RetryAfterSeconds() != 10 {
		t.Fatalf("the seventh call must be denied and told to wait 10s: %+v", d)
	}
}

func TestMemoryRefillsOverTime(t *testing.T) {
	fake := clock.NewFake(t0)
	m := NewMemory(fake, 0)
	for range 6 {
		allow(t, m, "k", 1)
	}
	fake.Advance(9 * time.Second)
	if allow(t, m, "k", 1).Allowed {
		t.Fatal("9s is not enough for a token")
	}
	fake.Advance(time.Second)
	if d := allow(t, m, "k", 1); !d.Allowed {
		t.Fatalf("10s refills one token: %+v", d)
	}
	fake.Advance(time.Hour)
	if d := allow(t, m, "k", 0); !d.Allowed || d.Remaining != 6 || d.ResetAfter != 0 {
		t.Fatalf("a long idle period refills to the cap, never beyond: %+v", d)
	}
}

func TestZeroCostPeeksWithoutConsuming(t *testing.T) {
	m := NewMemory(clock.NewFake(t0), 0)
	for range 3 {
		if d := allow(t, m, "k", 0); !d.Allowed || d.Remaining != 6 {
			t.Fatalf("peeking must not consume: %+v", d)
		}
	}
	for range 6 {
		allow(t, m, "k", 1)
	}
	if d := allow(t, m, "k", 0); d.Allowed {
		t.Fatalf("an empty bucket must be reported by a peek: %+v", d)
	}
}

func TestKeysAreIndependent(t *testing.T) {
	m := NewMemory(clock.NewFake(t0), 0)
	for range 6 {
		allow(t, m, "a", 1)
	}
	if allow(t, m, "a", 1).Allowed || !allow(t, m, "b", 1).Allowed {
		t.Fatal("one key's exhaustion must not affect another")
	}
}

func TestMemoryIsBounded(t *testing.T) {
	m := NewMemory(clock.NewFake(t0), 50)
	for i := range 500 {
		allow(t, m, "k"+strconv.Itoa(i), 1)
	}
	if m.Len() > 50 {
		t.Fatalf("held %d buckets, want at most 50", m.Len())
	}
}

func TestMemorySweepsRefilledBuckets(t *testing.T) {
	fake := clock.NewFake(t0)
	m := NewMemory(fake, 0)
	for i := range 100 {
		allow(t, m, "old"+strconv.Itoa(i), 1)
	}
	fake.Advance(time.Hour) // every bucket is full again
	for i := range sweepEvery {
		allow(t, m, "new"+strconv.Itoa(i%3), 1)
	}
	if m.Len() > 3 {
		t.Fatalf("refilled buckets should have been swept, %d remain", m.Len())
	}
}

func TestMemoryHonoursACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewMemory(clock.NewFake(t0), 0).Allow(ctx, "k", perMinute, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestRuleValidityAndHeaderHelpers(t *testing.T) {
	for rule, want := range map[Rule]bool{{6, time.Minute}: true, {0, time.Minute}: false, {1, 0}: false, {-1, time.Second}: false} {
		if rule.Valid() != want {
			t.Errorf("%+v valid = %v, want %v", rule, !want, want)
		}
	}
	d := Decision{RetryAfter: 1500 * time.Millisecond, ResetAfter: 2100 * time.Millisecond}
	if d.RetryAfterSeconds() != 2 || d.ResetSeconds() != 3 {
		t.Fatalf("rounding up: retry %d reset %d", d.RetryAfterSeconds(), d.ResetSeconds())
	}
	if (Decision{}).RetryAfterSeconds() != 1 || (Decision{}).ResetSeconds() != 0 {
		t.Fatal("a denial always asks for at least one second; a full bucket has no reset")
	}
}

// scripted is a Limiter that fails on demand.
type scripted struct {
	err   error
	calls int
	rules []Rule
}

func (s *scripted) Allow(_ context.Context, _ string, r Rule, _ int) (Decision, error) {
	s.calls++
	s.rules = append(s.rules, r)
	if s.err != nil {
		return Decision{}, s.err
	}
	return Decision{Allowed: true, Limit: r.Limit}, nil
}

func TestFallbackUsesStricterLocalLimitsWhileRedisIsDown(t *testing.T) {
	fake := clock.NewFake(t0)
	logs := &bytes.Buffer{}
	primary := &scripted{err: errors.New("connection refused")}
	local := &scripted{}
	f := NewFallback(primary, local, fake, slog.New(slog.NewTextHandler(logs, nil)), 0.5, 5*time.Second)

	for range 3 {
		if _, err := f.Allow(context.Background(), "k", perMinute, 1); err != nil {
			t.Fatal(err)
		}
	}
	if primary.calls != 1 {
		t.Fatalf("after a failure the primary must be skipped for a while, it was called %d times", primary.calls)
	}
	if local.rules[0].Limit != 3 {
		t.Fatalf("local limit = %d, want half of 6", local.rules[0].Limit)
	}
	if strings.Count(logs.String(), "fell back") != 1 {
		t.Fatalf("the transition must be logged once:\n%s", logs.String())
	}

	fake.Advance(5 * time.Second) // primary is tried again and is healthy
	primary.err = nil
	d, err := f.Allow(context.Background(), "k", perMinute, 1)
	if err != nil || d.Limit != 6 {
		t.Fatalf("recovered: %+v, %v (the full limit applies again)", d, err)
	}
	if !strings.Contains(logs.String(), "using Redis again") {
		t.Fatalf("recovery must be logged:\n%s", logs.String())
	}
}

func TestFallbackKeepsFailingQuietlyAndNeverBelowOne(t *testing.T) {
	fake := clock.NewFake(t0)
	logs := &bytes.Buffer{}
	primary := &scripted{err: errors.New("down")}
	f := NewFallback(primary, &scripted{}, fake, slog.New(slog.NewTextHandler(logs, nil)), 5 /* invalid: default */, time.Second)
	for range 3 {
		fake.Advance(2 * time.Second) // primary is retried each time and fails again
		if _, err := f.Allow(context.Background(), "k", Rule{Limit: 1, Window: time.Minute}, 1); err != nil {
			t.Fatal(err)
		}
	}
	if primary.calls != 3 || strings.Count(logs.String(), "fell back") != 1 {
		t.Fatalf("calls=%d logs:\n%s", primary.calls, logs.String())
	}
}

func TestFallbackSurfacesALocalFailure(t *testing.T) {
	boom := errors.New("boom")
	f := NewFallback(&scripted{err: errors.New("down")}, &scripted{err: boom}, clock.NewFake(t0), slog.New(slog.DiscardHandler), 0.5, time.Second)
	if _, err := f.Allow(context.Background(), "k", perMinute, 1); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}
