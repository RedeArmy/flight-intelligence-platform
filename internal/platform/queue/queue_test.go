package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func validJob() Job {
	return Job{Queue: "monitoring", Kind: "monitoring.observe", Payload: []byte(`{"route":"x"}`)}
}

func TestJobValidate(t *testing.T) {
	if err := validJob().Validate(); err != nil {
		t.Fatalf("a valid job was refused: %v", err)
	}
	cases := map[string]func(j *Job){
		"empty queue":          func(j *Job) { j.Queue = "" },
		"upper-case queue":     func(j *Job) { j.Queue = "Monitoring" },
		"queue with a space":   func(j *Job) { j.Queue = "a b" },
		"queue starting digit": func(j *Job) { j.Queue = "1abc" },
		"too long queue":       func(j *Job) { j.Queue = "a" + strings.Repeat("b", 64) },
		"empty kind":           func(j *Job) { j.Kind = "" },
		"kind with a slash":    func(j *Job) { j.Kind = "a/b" },
		"payload too big":      func(j *Job) { j.Payload = make([]byte, MaxPayloadBytes+1) },
		"bad idempotency key":  func(j *Job) { j.IdempotencyKey = "has space" },
		"long idempotency key": func(j *Job) { j.IdempotencyKey = strings.Repeat("k", 129) },
		"negative attempts":    func(j *Job) { j.MaxAttempts = -1 },
		"too many attempts":    func(j *Job) { j.MaxAttempts = MaxAttemptsLimit + 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			j := validJob()
			mutate(&j)
			if err := j.Validate(); err == nil {
				t.Fatal("must be refused")
			}
		})
	}
	edge := validJob()
	edge.Payload = make([]byte, MaxPayloadBytes)
	edge.IdempotencyKey = "monitor:target-42@2026-10-01T12:00"
	edge.MaxAttempts = MaxAttemptsLimit
	if err := edge.Validate(); err != nil {
		t.Fatalf("limits are inclusive: %v", err)
	}
}

func TestAttemptsDefault(t *testing.T) {
	j := validJob()
	if j.Attempts() != DefaultMaxAttempts {
		t.Fatalf("unset = %d", j.Attempts())
	}
	j.MaxAttempts = 3
	if j.Attempts() != 3 {
		t.Fatalf("set = %d", j.Attempts())
	}
}

func TestPermanentErrors(t *testing.T) {
	if Permanent(nil) != nil {
		t.Fatal("Permanent(nil) must be nil")
	}
	base := errors.New("unknown job kind")
	p := Permanent(base)
	if !IsPermanent(p) || !errors.Is(p, base) || p.Error() != base.Error() {
		t.Fatalf("a permanent error must keep its cause and message: %v", p)
	}
	if !IsPermanent(fmt.Errorf("handling job 7: %w", p)) {
		t.Fatal("wrapping must not hide the mark")
	}
	if IsPermanent(base) || IsPermanent(nil) {
		t.Fatal("an ordinary error is retried")
	}
}

func TestPolicyDelay(t *testing.T) {
	p := Policy{Base: 4 * time.Second, Max: 30 * time.Second}
	want := []time.Duration{4, 8, 16, 30, 30} // seconds, with no jitter reduction
	for i, w := range want {
		if got := p.Delay(i+1, 0); got != w*time.Second {
			t.Errorf("attempt %d = %v, want %v", i+1, got, w*time.Second)
		}
	}
	if p.Delay(0, 0) != p.Delay(1, 0) || p.Delay(-3, 0) != p.Delay(1, 0) {
		t.Error("attempts below one count as the first")
	}
	// Jitter spreads over the upper half: between half the delay and the full delay.
	for _, j := range []float64{0, 0.25, 0.5, 0.999, 5, -1} {
		got := p.Delay(2, j)
		if got < 4*time.Second || got > 8*time.Second {
			t.Errorf("jitter %v gives %v, outside [4s, 8s]", j, got)
		}
	}
	if p.Delay(2, 0.5) >= p.Delay(2, 0) {
		t.Error("more jitter must give a shorter delay")
	}
	if got := DefaultPolicy.Delay(100, 0); got != 15*time.Minute {
		t.Errorf("the cap must hold for a huge attempt number: %v", got)
	}
}

func TestHandlerFunc(t *testing.T) {
	var got Job
	var h Handler = HandlerFunc(func(_ context.Context, j Job) error { got = j; return Permanent(errors.New("no")) })
	err := h.Handle(context.Background(), validJob())
	if got.Kind != "monitoring.observe" || !IsPermanent(err) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// The port must stay implementable without importing anything but the standard library.
var _ JobQueue = (*noQueue)(nil)

type noQueue struct{}

func (noQueue) Publish(context.Context, Job) (Job, error)      { return Job{}, nil }
func (noQueue) Consume(context.Context, string, Handler) error { return nil }
