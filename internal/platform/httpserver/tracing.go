package httpserver

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/logging"
	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/observability/telemetry"
)

// Instrumentation carries what the HTTP layer needs to emit traces and metrics. A nil *Instrumentation turns
// instrumentation off (unit tests).
type Instrumentation struct {
	Tracer     trace.Tracer
	Propagator propagation.TextMapPropagator
	Metrics    *telemetry.Metrics
}

// metrics returns the metrics of a possibly nil Instrumentation.
func (in *Instrumentation) metrics() *telemetry.Metrics {
	if in == nil {
		return nil
	}
	return in.Metrics
}

// unmatchedRoute names requests that matched no route, so unknown paths cannot create unbounded label values.
const unmatchedRoute = "unmatched"

// tracing records one server span and the request metrics for every request.
//
// The span is a NEW ROOT even when the request carries a traceparent: the caller's trace is attached as a link
// instead of becoming the parent. Public callers are untrusted, and a parent decision would let them force sampling
// (cost) or inject their own trace IDs into our traces. The span is named after the route template once routing has
// run, and never contains the raw path, query string, headers or body.
func tracing(in *Instrumentation) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if in == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx, span := in.Tracer.Start(r.Context(), "HTTP "+r.Method, in.spanOptions(r)...)
			in.Metrics.RequestStarted(ctx)

			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r.WithContext(ctx))

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			route := routeTemplate(r)
			span.SetName(r.Method + " " + route)
			span.SetAttributes(attribute.String("http.route", route), attribute.Int("http.response.status_code", status))
			if status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, "") // no message: error text could carry internal detail
			}
			span.End()
			in.Metrics.RequestFinished(ctx, r.Method, route, status, time.Since(start).Seconds())
		})
	}
}

// spanOptions describes the server span of a request: a new root, linked to the caller's trace when one was sent.
func (in *Instrumentation) spanOptions(r *http.Request) []trace.SpanStartOption {
	opts := []trace.SpanStartOption{
		trace.WithNewRoot(),
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", r.Method)),
	}
	if id := logging.RequestID(r.Context()); id != "" {
		opts = append(opts, trace.WithAttributes(attribute.String("request.id", id)))
	}
	if remote := trace.SpanContextFromContext(in.Propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))); remote.IsValid() {
		opts = append(opts, trace.WithLinks(trace.Link{SpanContext: remote}))
	}
	return opts
}

// routeTemplate returns the matched route pattern, or unmatchedRoute.
func routeTemplate(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
		return rc.RoutePattern()
	}
	return unmatchedRoute
}
