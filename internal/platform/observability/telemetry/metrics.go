package telemetry

import (
	"context"
	"fmt"
	"runtime"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// All recording methods are safe on a nil *Metrics, which records nothing.
//
// Metric names follow docs/operations/reliability-and-observability.md. After translation by the collector's
// Prometheus exporter they read http_server_requests_total, http_server_duration_seconds, auth_failures_total,
// rate_limited_total, readiness_state and db_pool_*. Labels are bounded: route templates (never raw paths), status
// codes, and short fixed vocabularies. Never add a label with an unbounded value such as a client or offer ID.
const (
	metricRequests    = "http.server.requests"
	metricDuration    = "http.server.duration"
	metricActive      = "http.server.active_requests"
	metricAuthFail    = "auth.failures"
	metricRateLimited = "rate.limited"
)

// UCUM annotation units: counts of things, written in braces.
const (
	unitRequest    = "{request}"
	unitConnection = "{connection}"
)

// durationBuckets are the latency histogram boundaries in seconds.
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Metrics holds the instruments the platform records.
type Metrics struct {
	requests    metric.Int64Counter
	duration    metric.Float64Histogram
	active      metric.Int64UpDownCounter
	authFail    metric.Int64Counter
	rateLimited metric.Int64Counter
}

// NewMetrics creates the instruments on meter.
func NewMetrics(meter metric.Meter) (*Metrics, error) {
	var (
		m   Metrics
		err error
	)
	if m.requests, err = meter.Int64Counter(metricRequests,
		metric.WithUnit(unitRequest), metric.WithDescription("HTTP requests handled, by method, route and status")); err != nil {
		return nil, fmt.Errorf("telemetry: %s: %w", metricRequests, err)
	}
	if m.duration, err = meter.Float64Histogram(metricDuration,
		metric.WithUnit("s"), metric.WithDescription("HTTP request duration"),
		metric.WithExplicitBucketBoundaries(durationBuckets...)); err != nil {
		return nil, fmt.Errorf("telemetry: %s: %w", metricDuration, err)
	}
	if m.active, err = meter.Int64UpDownCounter(metricActive,
		metric.WithUnit(unitRequest), metric.WithDescription("HTTP requests in flight")); err != nil {
		return nil, fmt.Errorf("telemetry: %s: %w", metricActive, err)
	}
	if m.authFail, err = meter.Int64Counter(metricAuthFail,
		metric.WithUnit("{failure}"), metric.WithDescription("Failed authentications, by reason class")); err != nil {
		return nil, fmt.Errorf("telemetry: %s: %w", metricAuthFail, err)
	}
	if m.rateLimited, err = meter.Int64Counter(metricRateLimited,
		metric.WithUnit(unitRequest), metric.WithDescription("Requests refused by a rate limit, by limit")); err != nil {
		return nil, fmt.Errorf("telemetry: %s: %w", metricRateLimited, err)
	}
	return &m, nil
}

// RequestStarted counts a request as in flight.
func (m *Metrics) RequestStarted(ctx context.Context) {
	if m != nil {
		m.active.Add(ctx, 1)
	}
}

// RequestFinished records a completed request and removes it from the in-flight count.
func (m *Metrics) RequestFinished(ctx context.Context, method, route string, status int, seconds float64) {
	if m == nil {
		return
	}
	m.active.Add(ctx, -1)
	attrs := metric.WithAttributes(
		attribute.String("http.request.method", method),
		attribute.String("http.route", route),
		attribute.Int("http.response.status_code", status),
	)
	m.requests.Add(ctx, 1, attrs)
	m.duration.Record(ctx, seconds, attrs)
}

// AuthFailure counts a failed authentication. reasonClass is a short fixed vocabulary, never the reason text.
func (m *Metrics) AuthFailure(ctx context.Context, reasonClass string) {
	if m == nil {
		return
	}
	m.authFail.Add(ctx, 1, metric.WithAttributes(attribute.String("reason_class", reasonClass)))
}

// RateLimited counts a request refused by the named limit (ip, auth_failure or client).
func (m *Metrics) RateLimited(ctx context.Context, limit string) {
	if m == nil {
		return
	}
	m.rateLimited.Add(ctx, 1, metric.WithAttributes(attribute.String("limit", limit)))
}

// PoolStats is a snapshot of a database connection pool.
type PoolStats struct {
	Acquired int32 // connections in use
	Idle     int32
	Total    int32
	Max      int32
	// EmptyAcquires counts acquisitions that had to wait because the pool was empty (saturation).
	EmptyAcquires int64
}

// RegisterPool exposes pool saturation as gauges.
func RegisterPool(meter metric.Meter, stats func() PoolStats) error {
	acquired, err := meter.Int64ObservableGauge("db.pool.acquired_connections", metric.WithUnit(unitConnection))
	if err != nil {
		return fmt.Errorf("telemetry: db.pool.acquired_connections: %w", err)
	}
	idle, err := meter.Int64ObservableGauge("db.pool.idle_connections", metric.WithUnit(unitConnection))
	if err != nil {
		return fmt.Errorf("telemetry: db.pool.idle_connections: %w", err)
	}
	maxConns, err := meter.Int64ObservableGauge("db.pool.max_connections", metric.WithUnit(unitConnection))
	if err != nil {
		return fmt.Errorf("telemetry: db.pool.max_connections: %w", err)
	}
	waits, err := meter.Int64ObservableCounter("db.pool.empty_acquires", metric.WithUnit("{acquire}"),
		metric.WithDescription("Acquisitions that waited because every connection was busy"))
	if err != nil {
		return fmt.Errorf("telemetry: db.pool.empty_acquires: %w", err)
	}
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := stats()
		o.ObserveInt64(acquired, int64(s.Acquired))
		o.ObserveInt64(idle, int64(s.Idle))
		o.ObserveInt64(maxConns, int64(s.Max))
		o.ObserveInt64(waits, s.EmptyAcquires)
		return nil
	}, acquired, idle, maxConns, waits)
	return err
}

// Readiness states as numbers, so a dashboard can alert on a value: not_ready 0, degraded 1, ready 2.
var readinessValue = map[string]int64{"not_ready": 0, "degraded": 1, "ready": 2}

// RegisterReadiness exposes the readiness state as a gauge. state is called on every collection, so it must be cheap
// and bounded in time (the readiness checks already run under their own timeout).
func RegisterReadiness(meter metric.Meter, state func(ctx context.Context) string) error {
	g, err := meter.Int64ObservableGauge("readiness.state",
		metric.WithDescription("Readiness: 0 not_ready, 1 degraded, 2 ready"))
	if err != nil {
		return fmt.Errorf("telemetry: readiness.state: %w", err)
	}
	_, err = meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		v, ok := readinessValue[state(ctx)]
		if !ok {
			v = 0
		}
		o.ObserveInt64(g, v)
		return nil
	}, g)
	return err
}

// RegisterRuntime exposes a few Go runtime gauges.
func RegisterRuntime(meter metric.Meter) error {
	goroutines, err := meter.Int64ObservableGauge("go.goroutines", metric.WithUnit("{goroutine}"))
	if err != nil {
		return fmt.Errorf("telemetry: go.goroutines: %w", err)
	}
	heap, err := meter.Int64ObservableGauge("go.memory.heap_in_use", metric.WithUnit("By"))
	if err != nil {
		return fmt.Errorf("telemetry: go.memory.heap_in_use: %w", err)
	}
	gcs, err := meter.Int64ObservableCounter("go.gc.cycles", metric.WithUnit("{cycle}"))
	if err != nil {
		return fmt.Errorf("telemetry: go.gc.cycles: %w", err)
	}
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		o.ObserveInt64(goroutines, int64(runtime.NumGoroutine()))
		o.ObserveInt64(heap, int64(min(ms.HeapInuse, 1<<62))) // #nosec G115 -- clamped below the int64 limit
		o.ObserveInt64(gcs, int64(ms.NumGC))
		return nil
	}, goroutines, heap, gcs)
	return err
}
