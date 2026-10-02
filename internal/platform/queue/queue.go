// Package queue defines the contract of the background job queue (ADR-012, ADR-013): what a job is, what a handler
// returns to say what should happen to it, and the port the first adapter (PostgreSQL, slice E8) will implement.
//
// There is deliberately no adapter and no consumer loop here. The package fixes the vocabulary now so that handlers
// written later, and the adapter behind the port, cannot disagree about delivery guarantees:
//
//   - Delivery is at least once. A handler may run more than once for the same job and must be idempotent.
//   - A handler returning nil acknowledges the job.
//   - A handler returning any other error asks for a retry, with backoff, until MaxAttempts is reached.
//   - A handler returning Permanent(err) says retrying cannot help: the job goes to the dead-letter queue at once.
//   - A job that exhausts its attempts goes to the dead-letter queue.
package queue

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Limits on what a job may carry. They keep rows small and queue names and kinds safe to log and to use as labels.
const (
	// MaxPayloadBytes is the largest payload accepted. Large data belongs in the database; a job carries references.
	MaxPayloadBytes = 64 << 10
	// MaxAttemptsLimit is the largest MaxAttempts accepted.
	MaxAttemptsLimit = 50
	// DefaultMaxAttempts applies when a job does not set MaxAttempts.
	DefaultMaxAttempts = 5
)

var (
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{1,63}$`)
	keyPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,127}$`)
)

// Job is one unit of background work.
type Job struct {
	// ID is assigned by the queue when the job is published. Empty on publish.
	ID string
	// Queue names the queue the job belongs to, for example "monitoring". Lower case, bounded.
	Queue string
	// Kind says which handler runs the job, for example "monitoring.observe".
	Kind string
	// Payload is the job's input, at most MaxPayloadBytes. It must not contain secrets.
	Payload []byte
	// IdempotencyKey, when set, makes publishing the same key twice enqueue one job.
	IdempotencyKey string
	// RunAt is the earliest time the job may run. Zero means now.
	RunAt time.Time
	// MaxAttempts is how many times the job may be tried. Zero means DefaultMaxAttempts.
	MaxAttempts int
	// Attempt is how many times the job has been delivered, counting the current delivery. Set by the queue.
	Attempt int
	// Metadata carries trace context and correlation IDs across the queue (W3C traceparent, correlation_id). Never
	// credentials or personal data.
	Metadata map[string]string
}

// Validate reports the first problem that would make a queue refuse the job when it is published.
func (j Job) Validate() error {
	switch {
	case !namePattern.MatchString(j.Queue):
		return fmt.Errorf("queue: invalid queue name %q", j.Queue)
	case !namePattern.MatchString(j.Kind):
		return fmt.Errorf("queue: invalid job kind %q", j.Kind)
	case len(j.Payload) > MaxPayloadBytes:
		return fmt.Errorf("queue: payload is %d bytes, the limit is %d", len(j.Payload), MaxPayloadBytes)
	case j.IdempotencyKey != "" && !keyPattern.MatchString(j.IdempotencyKey):
		return errors.New("queue: invalid idempotency key")
	case j.MaxAttempts < 0 || j.MaxAttempts > MaxAttemptsLimit:
		return fmt.Errorf("queue: MaxAttempts must be between 0 and %d", MaxAttemptsLimit)
	}
	return nil
}

// Attempts returns MaxAttempts, or DefaultMaxAttempts when it is unset.
func (j Job) Attempts() int {
	if j.MaxAttempts == 0 {
		return DefaultMaxAttempts
	}
	return j.MaxAttempts
}

// Handler runs one job. See the package documentation for what its result means.
type Handler interface {
	Handle(ctx context.Context, job Job) error
}

// HandlerFunc adapts a function to a Handler.
type HandlerFunc func(ctx context.Context, job Job) error

// Handle calls f.
func (f HandlerFunc) Handle(ctx context.Context, job Job) error { return f(ctx, job) }

// JobQueue is the port. Publishing may be done inside a database transaction by an adapter that supports it
// (transactional enqueue, ADR-013); the port itself does not promise that.
type JobQueue interface {
	// Publish validates the job and enqueues it. It returns the job with its ID set. Publishing a job whose
	// IdempotencyKey already exists returns the existing job and no error.
	Publish(ctx context.Context, job Job) (Job, error)
	// Consume delivers jobs of the named queue to h until ctx is cancelled, applying the acknowledge, retry and
	// dead-letter rules of the package documentation. It returns when ctx is done and in-flight jobs have finished.
	Consume(ctx context.Context, queue string, h Handler) error
}

// permanentError marks an error that retrying cannot fix.
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent wraps err to tell the queue not to retry: the job goes to the dead-letter queue at once. Use it for
// problems in the job itself (an unknown kind, an invalid payload), not for temporary failures of a dependency.
// Permanent(nil) is nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

// IsPermanent reports whether err, or an error it wraps, was marked with Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// Policy decides how long to wait before the next attempt.
type Policy struct {
	Base time.Duration // delay before the second attempt
	Max  time.Duration // upper bound of any delay
}

// DefaultPolicy waits 5 seconds, doubling each time, never more than 15 minutes.
var DefaultPolicy = Policy{Base: 5 * time.Second, Max: 15 * time.Minute}

// Delay returns how long to wait after the given failed attempt (1 for the first failure): exponential backoff
// capped at Max, then spread over the upper half of that value by jitter in [0, 1) so that jobs that failed together
// do not retry together. Pass 0 for the longest delay (deterministic, for tests) or a random number in production.
func (p Policy) Delay(attempt int, jitter float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := p.Base
	for i := 1; i < attempt && d < p.Max; i++ {
		d *= 2
	}
	d = min(d, p.Max)
	jitter = min(max(jitter, 0), 0.999999)
	return d - time.Duration(float64(d/2)*jitter)
}
