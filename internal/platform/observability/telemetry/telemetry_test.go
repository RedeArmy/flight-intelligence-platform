package telemetry

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

func TestWithoutAnEndpointSpansExistButNothingIsExported(t *testing.T) {
	tel, err := New(context.Background(), Options{Service: "api", Version: "1", Env: "test", SampleRatio: 1, MetricInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := tel.Tracer().Start(context.Background(), "op")
	traceID, spanID := TraceIDs(ctx)
	span.End()
	if len(traceID) != 32 || len(spanID) != 16 {
		t.Fatalf("logs need real IDs even without export: trace %q span %q", traceID, spanID)
	}
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown without exporters: %v", err)
	}
}

func TestTraceIDsWithoutASpan(t *testing.T) {
	if traceID, spanID := TraceIDs(context.Background()); traceID != "" || spanID != "" {
		t.Fatalf("got %q %q", traceID, spanID)
	}
}

// collector is a stand-in OTLP/HTTP receiver that records what it is sent.
type collector struct {
	mu     sync.Mutex
	bodies map[string][]byte
	srv    *httptest.Server
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{bodies: map[string][]byte{}}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies[r.URL.Path] = append(c.bodies[r.URL.Path], b...)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK) // an empty body is a valid, empty OTLP response
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *collector) received(path string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.bodies[path]...)
}

func TestWithAnEndpointTracesAndMetricsReachTheCollector(t *testing.T) {
	col := newCollector(t)
	tel, err := New(context.Background(), Options{
		Service: "api", Version: "9.9.9", Env: "test", Endpoint: col.srv.URL, SampleRatio: 1, MetricInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMetrics(tel.Meter())
	if err != nil {
		t.Fatal(err)
	}
	_, span := tel.Tracer().Start(context.Background(), "unit-test-span")
	span.End()
	m.AuthFailure(context.Background(), "malformed")

	if err := tel.Shutdown(context.Background()); err != nil { // shutdown flushes both pipelines
		t.Fatal(err)
	}
	traces, metrics := col.received("/v1/traces"), col.received("/v1/metrics")
	for _, want := range []string{"unit-test-span", "api", "9.9.9", "deployment.environment.name"} {
		if !bytes.Contains(traces, []byte(want)) {
			t.Errorf("traces payload lacks %q", want)
		}
	}
	if !bytes.Contains(metrics, []byte("auth.failures")) || !bytes.Contains(metrics, []byte("reason_class")) {
		t.Errorf("metrics payload lacks the auth failure counter (%d bytes)", len(metrics))
	}
}

func TestAnUnreachableCollectorNeverFailsStartupOrBlocksShutdown(t *testing.T) {
	tel, err := New(context.Background(), Options{
		Service: "api", Endpoint: "http://127.0.0.1:1", SampleRatio: 1, MetricInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("a missing collector must not fail startup: %v", err)
	}
	_, span := tel.Tracer().Start(context.Background(), "x")
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_ = tel.Shutdown(ctx) // may report the failed export; it must return within the bound
	if time.Since(start) > 4*time.Second {
		t.Fatalf("shutdown took %v", time.Since(start))
	}
}

func TestSamplingRatioZeroRecordsNothing(t *testing.T) {
	tel, err := New(context.Background(), Options{Service: "api", SampleRatio: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tel.Shutdown(context.Background()) }()
	_, span := tel.Tracer().Start(context.Background(), "x")
	defer span.End()
	if span.SpanContext().IsSampled() {
		t.Fatal("ratio 0 must not sample new traces")
	}
}

func TestPropagatorUnderstandsTraceContext(t *testing.T) {
	fields := Propagator().Fields()
	want := map[string]bool{"traceparent": true, "tracestate": true, "baggage": true}
	for _, f := range fields {
		delete(want, f)
	}
	if len(want) != 0 {
		t.Fatalf("missing propagation fields %v in %v", want, fields)
	}
}

// reader builds a meter on a manual reader so tests can read back what was recorded.
func reader(t *testing.T) (*sdkmetric.ManualReader, *Metrics, func() metricdata.ResourceMetrics) {
	t.Helper()
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	m, err := NewMetrics(mp.Meter("test"))
	if err != nil {
		t.Fatal(err)
	}
	collect := func() metricdata.ResourceMetrics {
		var rm metricdata.ResourceMetrics
		if err := r.Collect(context.Background(), &rm); err != nil {
			t.Fatal(err)
		}
		return rm
	}
	return r, m, collect
}

func find(rm metricdata.ResourceMetrics, name string) (metricdata.Metrics, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

func TestRequestMetricsAreRecordedWithBoundedLabels(t *testing.T) {
	_, m, collect := reader(t)
	ctx := context.Background()
	m.RequestStarted(ctx)
	m.RequestFinished(ctx, "GET", "/v1/whoami", 200, 0.02)
	m.RequestStarted(ctx)
	m.RequestFinished(ctx, "GET", "/v1/whoami", 200, 0.3)

	rm := collect()
	reqs, ok := find(rm, "http.server.requests")
	if !ok {
		t.Fatal("no request counter")
	}
	pts := reqs.Data.(metricdata.Sum[int64]).DataPoints
	if len(pts) != 1 || pts[0].Value != 2 {
		t.Fatalf("points = %+v", pts)
	}
	want := attribute.NewSet(attribute.String("http.request.method", "GET"), attribute.String("http.route", "/v1/whoami"), attribute.Int("http.response.status_code", 200))
	if !pts[0].Attributes.Equals(&want) {
		t.Fatalf("attributes = %v, want exactly method, route and status", pts[0].Attributes)
	}
	dur, _ := find(rm, "http.server.duration")
	hist := dur.Data.(metricdata.Histogram[float64]).DataPoints[0]
	if hist.Count != 2 || dur.Unit != "s" || len(hist.Bounds) != len(durationBuckets) {
		t.Fatalf("histogram = %+v unit %q", hist, dur.Unit)
	}
	active, _ := find(rm, "http.server.active_requests")
	if v := active.Data.(metricdata.Sum[int64]).DataPoints[0].Value; v != 0 {
		t.Fatalf("in flight after completion = %d, want 0", v)
	}
}

func TestAuthFailureAndRateLimitCounters(t *testing.T) {
	_, m, collect := reader(t)
	ctx := context.Background()
	m.AuthFailure(ctx, "inactive")
	m.AuthFailure(ctx, "inactive")
	m.RateLimited(ctx, "ip")

	rm := collect()
	af, _ := find(rm, "auth.failures")
	pt := af.Data.(metricdata.Sum[int64]).DataPoints[0]
	if v, _ := pt.Attributes.Value("reason_class"); pt.Value != 2 || v.AsString() != "inactive" {
		t.Fatalf("auth failures = %+v", pt)
	}
	rl, _ := find(rm, "rate.limited")
	pt = rl.Data.(metricdata.Sum[int64]).DataPoints[0]
	if v, _ := pt.Attributes.Value("limit"); pt.Value != 1 || v.AsString() != "ip" {
		t.Fatalf("rate limited = %+v", pt)
	}
}

func TestNilMetricsRecordNothingAndDoNotPanic(t *testing.T) {
	var m *Metrics
	ctx := context.Background()
	m.RequestStarted(ctx)
	m.RequestFinished(ctx, "GET", "/x", 200, 1)
	m.AuthFailure(ctx, "x")
	m.RateLimited(ctx, "x")
}

func gauge(t *testing.T, rm metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	m, ok := find(rm, name)
	if !ok {
		t.Fatalf("metric %s missing", name)
	}
	switch d := m.Data.(type) {
	case metricdata.Gauge[int64]:
		return d.DataPoints[0].Value
	case metricdata.Sum[int64]:
		return d.DataPoints[0].Value
	}
	t.Fatalf("metric %s has unexpected data %T", name, m.Data)
	return 0
}

func TestPoolReadinessAndRuntimeGauges(t *testing.T) {
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	defer func() { _ = mp.Shutdown(context.Background()) }()
	meter := mp.Meter("test")

	if err := RegisterPool(meter, func() PoolStats { return PoolStats{Acquired: 3, Idle: 2, Total: 5, Max: 10, EmptyAcquires: 7} }); err != nil {
		t.Fatal(err)
	}
	state := "degraded"
	if err := RegisterReadiness(meter, func(context.Context) string { return state }); err != nil {
		t.Fatal(err)
	}
	if err := RegisterRuntime(meter); err != nil {
		t.Fatal(err)
	}

	collect := func() metricdata.ResourceMetrics {
		var rm metricdata.ResourceMetrics
		if err := r.Collect(context.Background(), &rm); err != nil {
			t.Fatal(err)
		}
		return rm
	}
	rm := collect()
	for name, want := range map[string]int64{
		"db.pool.acquired_connections": 3, "db.pool.idle_connections": 2, "db.pool.max_connections": 10,
		"db.pool.empty_acquires": 7, "readiness.state": 1,
	} {
		if got := gauge(t, rm, name); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	if gauge(t, rm, "go.goroutines") < 1 || gauge(t, rm, "go.memory.heap_in_use") < 1 {
		t.Error("runtime gauges must report positive values")
	}

	for in, want := range map[string]int64{"ready": 2, "not_ready": 0, "something else": 0} {
		state = in
		if got := gauge(t, collect(), "readiness.state"); got != want {
			t.Errorf("state %q = %d, want %d", in, got, want)
		}
	}
}

var _ trace.Tracer // the package under test hands out standard OpenTelemetry tracers
