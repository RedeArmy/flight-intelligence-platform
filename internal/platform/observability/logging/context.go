package logging

import (
	"context"
	"log/slog"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	correlationIDKey
)

// WithRequestID returns a context that carries the request ID; logs written with it include request_id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the request ID stored in ctx, or "".
func RequestID(ctx context.Context) string { return stringFrom(ctx, requestIDKey) }

// WithCorrelationID returns a context that carries the correlation ID (a search or monitoring run).
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

// CorrelationID returns the correlation ID stored in ctx, or "".
func CorrelationID(ctx context.Context) string { return stringFrom(ctx, correlationIDKey) }

func stringFrom(ctx context.Context, key ctxKey) string {
	s, _ := ctx.Value(key).(string)
	return s
}

// TraceExtractor returns the trace and span IDs of the span in ctx. The OpenTelemetry slice (S5) supplies it
// so logs and traces correlate without this package importing the SDK.
type TraceExtractor func(ctx context.Context) (traceID, spanID string)

// contextHandler adds the correlation attributes found in the context to every record.
type contextHandler struct {
	next  slog.Handler
	trace TraceExtractor
}

func (h contextHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	if id := CorrelationID(ctx); id != "" {
		r.AddAttrs(slog.String("correlation_id", id))
	}
	if h.trace != nil {
		if traceID, spanID := h.trace(ctx); traceID != "" {
			r.AddAttrs(slog.String("trace_id", traceID), slog.String("span_id", spanID))
		}
	}
	return h.next.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{next: h.next.WithAttrs(attrs), trace: h.trace}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{next: h.next.WithGroup(name), trace: h.trace}
}
