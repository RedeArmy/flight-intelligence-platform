// Package telemetry sets up OpenTelemetry traces and metrics (ADR-019, ADR-033).
//
// Providers are built explicitly and handed to the code that needs them; nothing is registered globally. Without an
// OTLP endpoint no exporter exists, so nothing leaves the process and no connection errors appear, but spans are still
// created so every log line carries a trace ID.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// instrumentationName identifies this module as the source of the telemetry.
const instrumentationName = "github.com/RedeArmy/flight-intelligence-platform"

// Options configure New.
type Options struct {
	Service        string  // service.name, for example "api"
	Version        string  // service.version
	Env            string  // deployment.environment.name
	Endpoint       string  // OTLP/HTTP base URL; empty disables export
	SampleRatio    float64 // fraction of new traces recorded, 0 to 1
	MetricInterval time.Duration
	Insecure       bool // allow a plain http endpoint (the config layer already restricts this to non-production)
}

// Telemetry owns the trace and metric providers.
type Telemetry struct {
	tp *sdktrace.TracerProvider
	mp *sdkmetric.MeterProvider
}

// New builds the providers. It does not connect to the collector: exporters send in the background and a missing
// collector never blocks or fails the service.
func New(ctx context.Context, o Options) (*Telemetry, error) {
	res, err := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName(o.Service),
		semconv.ServiceVersion(o.Version),
		attribute.String("deployment.environment.name", o.Env),
	))
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		// Parent-based so a child follows its parent. Public routes start a new root for every request (see the
		// HTTP middleware), so a caller cannot force sampling by sending a traceparent.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(o.SampleRatio))),
	}
	mpOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}

	if o.Endpoint != "" {
		traceExp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(o.Endpoint+"/v1/traces"))
		if err != nil {
			return nil, fmt.Errorf("telemetry: trace exporter: %w", err)
		}
		metricExp, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(o.Endpoint+"/v1/metrics"))
		if err != nil {
			return nil, fmt.Errorf("telemetry: metric exporter: %w", err)
		}
		tpOpts = append(tpOpts, sdktrace.WithBatcher(traceExp))
		mpOpts = append(mpOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(o.MetricInterval))))
	}
	return &Telemetry{tp: sdktrace.NewTracerProvider(tpOpts...), mp: sdkmetric.NewMeterProvider(mpOpts...)}, nil
}

// TracerProvider returns the trace provider.
func (t *Telemetry) TracerProvider() trace.TracerProvider { return t.tp }

// Tracer returns the tracer for this module.
func (t *Telemetry) Tracer() trace.Tracer { return t.tp.Tracer(instrumentationName) }

// Meter returns the meter for this module.
func (t *Telemetry) Meter() metric.Meter { return t.mp.Meter(instrumentationName) }

// Propagator is the W3C trace context and baggage propagator.
func Propagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}

// Shutdown flushes pending telemetry and stops the providers. Call it after the server has stopped, with a bounded
// context: an unreachable collector must not delay exit.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	return errors.Join(t.tp.Shutdown(ctx), t.mp.Shutdown(ctx))
}

// TraceIDs returns the trace and span IDs of the span in ctx, or empty strings when there is none. It is the
// logging package's TraceExtractor, so logs and traces correlate (ADR-019).
func TraceIDs(ctx context.Context) (traceID, spanID string) {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return "", ""
	}
	return sc.TraceID().String(), sc.SpanID().String()
}
