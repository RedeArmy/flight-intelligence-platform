package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/access"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/telemetry"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/ratelimit"
	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/clock"
)

// obsRig records spans and metrics in memory.
type obsRig struct {
	spans   *tracetest.SpanRecorder
	reader  *sdkmetric.ManualReader
	inst    *Instrumentation
	logs    *bytes.Buffer
	logger  *slog.Logger
	metrics *telemetry.Metrics
}

func newObsRig(t *testing.T) *obsRig {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	metrics, err := telemetry.NewMetrics(mp.Meter("test"))
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	logger, err := logging.New(logging.Options{Service: "test", Env: "test", Version: "0", Level: "debug", Out: logs, Trace: telemetry.TraceIDs})
	if err != nil {
		t.Fatal(err)
	}
	return &obsRig{
		spans: rec, reader: reader, logs: logs, logger: logger, metrics: metrics,
		inst: &Instrumentation{Tracer: tp.Tracer("test"), Propagator: telemetry.Propagator(), Metrics: metrics},
	}
}

func (o *obsRig) handler(auth Authenticator) http.Handler {
	return NewPublicHandler(PublicDeps{Logger: o.logger, Auth: auth, Health: NewHealth(time.Second), MaxBodyBytes: 1 << 10, Telemetry: o.inst})
}

func (o *obsRig) collect(t *testing.T) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := o.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	return rm
}

func (o *obsRig) ended(t *testing.T) []sdktrace.ReadOnlySpan {
	t.Helper()
	return o.spans.Ended()
}

func TestEachRequestProducesOneServerSpanNamedAfterTheRoute(t *testing.T) {
	o := newObsRig(t)
	auth := &fakeAuth{principal: access.Principal{ClientID: "c1", Role: access.RoleDeveloper}}
	req := httptest.NewRequest(http.MethodGet, "/v1/whoami?secret=abc&email=a@b.c", nil)
	req.Header.Set("Authorization", "Bearer sk")
	req.Header.Set("X-Request-Id", "req-trace-0001")
	rec := httptest.NewRecorder()
	o.handler(auth).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	spans := o.ended(t)
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	s := spans[0]
	if s.Name() != "GET /v1/whoami" || s.SpanKind() != trace.SpanKindServer || s.Status().Code == codes.Error {
		t.Fatalf("span = %q kind %v status %v", s.Name(), s.SpanKind(), s.Status())
	}
	attrs := map[string]attribute.Value{}
	for _, kv := range s.Attributes() {
		attrs[string(kv.Key)] = kv.Value
	}
	if attrs["http.route"].AsString() != "/v1/whoami" || attrs["http.response.status_code"].AsInt64() != 200 || attrs["request.id"].AsString() != "req-trace-0001" {
		t.Fatalf("attributes = %v", attrs)
	}
	for k, v := range attrs {
		for _, leak := range []string{"secret=abc", "a@b.c", "Bearer", "sk"} {
			if v.Type() == attribute.STRING && strings.Contains(v.AsString(), leak) && v.AsString() != "req-trace-0001" {
				t.Errorf("attribute %s = %q leaks request data (%q)", k, v.AsString(), leak)
			}
		}
	}
}

func TestAnInboundTraceparentIsLinkedNeverTrusted(t *testing.T) {
	o := newObsRig(t)
	const remoteTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
	req := authedRequest("/v1/whoami")
	req.Header.Set("traceparent", "00-"+remoteTrace+"-00f067aa0ba902b7-01")
	o.handler(&fakeAuth{principal: access.Principal{ClientID: "c", Role: access.RoleDeveloper}}).ServeHTTP(httptest.NewRecorder(), req)

	s := o.ended(t)[0]
	if s.SpanContext().TraceID().String() == remoteTrace || s.Parent().IsValid() {
		t.Fatalf("a caller must not choose our trace ID or parent: trace %s parent valid %v", s.SpanContext().TraceID(), s.Parent().IsValid())
	}
	links := s.Links()
	if len(links) != 1 || links[0].SpanContext.TraceID().String() != remoteTrace {
		t.Fatalf("the caller's trace must be attached as a link: %+v", links)
	}
}

func TestServerErrorsMarkTheSpanWithoutAMessage(t *testing.T) {
	o := newObsRig(t)
	r := newBaseRouter(o.logger, o.inst)
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("kaboom: internal detail") })
	routePolicies["GET /boom"] = routePolicy{public: true}
	t.Cleanup(func() { delete(routePolicies, "GET /boom") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	s := o.ended(t)[0]
	if s.Status().Code != codes.Error || s.Status().Description != "" {
		t.Fatalf("status = %+v, want Error with no description", s.Status())
	}
	if s.Name() != "GET /boom" {
		t.Fatalf("name = %q", s.Name())
	}
}

func TestUnknownPathsUseOneBoundedRouteLabel(t *testing.T) {
	o := newObsRig(t)
	h := o.handler(nil)
	for _, p := range []string{"/nope/1", "/nope/2", "/other"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	for _, s := range o.ended(t) {
		if s.Name() != "GET unmatched" {
			t.Errorf("span name %q must not contain the raw path", s.Name())
		}
	}
	var seen []string
	for _, sm := range o.collect(t).ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.requests" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				v, _ := dp.Attributes.Value("http.route")
				seen = append(seen, v.AsString())
			}
		}
	}
	if len(seen) != 1 || seen[0] != "unmatched" {
		t.Fatalf("route labels = %v, want the single value %q (cardinality guard)", seen, "unmatched")
	}
}

func TestLogLinesCarryTheTraceIDOfTheirRequest(t *testing.T) {
	o := newObsRig(t)
	o.handler(&fakeAuth{principal: access.Principal{ClientID: "c", Role: access.RoleDeveloper}}).ServeHTTP(
		httptest.NewRecorder(), authedRequest("/v1/whoami"))

	span := o.ended(t)[0]
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(o.logs.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec["msg"] != "request" {
			continue
		}
		found = true
		if rec["trace_id"] != span.SpanContext().TraceID().String() || rec["span_id"] != span.SpanContext().SpanID().String() {
			t.Fatalf("access log %v does not match span %s/%s", rec, span.SpanContext().TraceID(), span.SpanContext().SpanID())
		}
	}
	if !found {
		t.Fatalf("no access log record:\n%s", o.logs.String())
	}
}

func authedRequest(path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer token")
	return r
}

func TestRequestMetricsHaveRouteAndStatus(t *testing.T) {
	o := newObsRig(t)
	h := o.handler(&fakeAuth{principal: access.Principal{ClientID: "c", Role: access.RoleDeveloper}})
	h.ServeHTTP(httptest.NewRecorder(), authedRequest("/v1/whoami"))
	h.ServeHTTP(httptest.NewRecorder(), authedRequest("/v1/whoami"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil)) // a probe: not recorded

	got := map[string]int64{}
	for _, sm := range o.collect(t).ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.requests" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				route, _ := dp.Attributes.Value("http.route")
				status, _ := dp.Attributes.Value("http.response.status_code")
				got[route.AsString()+" "+strings.TrimSpace(status.String())] = dp.Value
			}
		}
	}
	if got["/v1/whoami 200"] != 2 || len(got) != 1 {
		t.Fatalf("counts = %v", got)
	}
}

// counterTotal sums the data points of the named counter whose label matches (an empty label matches every point).
func (o *obsRig) counterTotal(t *testing.T, name, label, value string) int64 {
	t.Helper()
	var total int64
	for _, sm := range o.collect(t).ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				if v, _ := dp.Attributes.Value(attribute.Key(label)); label == "" || v.AsString() == value {
					total += dp.Value
				}
			}
		}
	}
	return total
}

func TestRateLimitedRequestsAreCounted(t *testing.T) {
	o := newObsRig(t)
	h := NewPublicHandler(PublicDeps{
		Logger: o.logger, Auth: &fakeAuth{principal: access.Principal{ClientID: "c", Role: access.RoleDeveloper}}, Health: NewHealth(time.Second),
		MaxBodyBytes: 1 << 10, Telemetry: o.inst,
		Limiter: ratelimit.NewMemory(clock.NewFake(guardT0), 0), Limits: Limits{IP: perHour(1)},
	})
	for range 3 {
		h.ServeHTTP(httptest.NewRecorder(), authedRequest("/v1/whoami"))
	}
	if got := o.counterTotal(t, "rate.limited", "limit", "ip"); got != 2 {
		t.Fatalf("rate_limited{limit=ip} = %d, want 2", got)
	}
}

func TestAPeekThatIsNotChargedDoesNotCountAsRateLimited(t *testing.T) {
	// A throttled address is counted once per refused request, not again when its failure would have been charged.
	o := newObsRig(t)
	h := NewPublicHandler(PublicDeps{
		Logger: o.logger, Auth: &fakeAuth{err: ErrUnauthenticated}, Health: NewHealth(time.Second),
		MaxBodyBytes: 1 << 10, Telemetry: o.inst,
		Limiter: ratelimit.NewMemory(clock.NewFake(guardT0), 0), Limits: Limits{AuthFailure: perHour(1)},
	})
	for range 4 {
		h.ServeHTTP(httptest.NewRecorder(), authedRequest("/v1/whoami"))
	}
	// Request 1 fails and is charged (allowed); request 2's peek finds the bucket empty: refused. 3 and 4 likewise.
	if got := o.counterTotal(t, "rate.limited", "", ""); got != 3 {
		t.Fatalf("rate limited = %d, want 3 refusals", got)
	}
}

func TestNilInstrumentationIsAPassThrough(t *testing.T) {
	called := false
	h := tracing(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Fatal("the wrapped handler must run")
	}
}

func TestProbesProduceNoSpansAndNoRequestMetrics(t *testing.T) {
	o := newObsRig(t)
	h := o.handler(nil)
	for _, path := range []string{"/healthz", "/readyz"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if n := len(o.ended(t)); n != 0 {
		t.Fatalf("probes produced %d spans", n)
	}
	if total := o.counterTotal(t, "http.server.requests", "", ""); total != 0 {
		t.Fatalf("probes counted %d requests", total)
	}
}
