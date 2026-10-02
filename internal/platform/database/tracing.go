package database

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// queryTracer records one client span per statement. It implements pgx.QueryTracer.
//
// A span carries the database system and the statement's verb (SELECT, INSERT, ...) and nothing else: never the SQL
// text, the arguments or the error message, because any of them may hold personal data or credentials (SR-09).
// Statements that run without a parent span (background work such as metric callbacks and pool health checks) are
// not traced, so they cannot flood the backend with one-span traces.
type queryTracer struct {
	tracer trace.Tracer
}

var _ pgx.QueryTracer = (*queryTracer)(nil)

type spanKey struct{}

// TraceQueryStart implements pgx.QueryTracer.
func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx
	}
	verb := sqlVerb(data.SQL)
	ctx, span := t.tracer.Start(ctx, "db "+verb,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("db.system.name", "postgresql"), attribute.String("db.operation.name", verb)))
	return context.WithValue(ctx, spanKey{}, span)
}

// TraceQueryEnd implements pgx.QueryTracer.
func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(spanKey{}).(trace.Span)
	if !ok {
		return
	}
	if data.Err != nil {
		span.SetStatus(codes.Error, "") // no message: it can echo statement text or values
	}
	span.End()
}

// sqlVerb returns the first word of a statement in upper case, or "QUERY" when it is not a plain word.
// Comments and leading whitespace are skipped.
func sqlVerb(sql string) string {
	s := strings.TrimSpace(sql)
	for strings.HasPrefix(s, "--") || strings.HasPrefix(s, "/*") {
		if strings.HasPrefix(s, "--") {
			_, rest, found := strings.Cut(s, "\n")
			if !found {
				return "QUERY"
			}
			s = strings.TrimSpace(rest)
			continue
		}
		_, rest, found := strings.Cut(s, "*/")
		if !found {
			return "QUERY"
		}
		s = strings.TrimSpace(rest)
	}
	word, _, _ := strings.Cut(s, " ")
	word = strings.ToUpper(strings.TrimSpace(strings.SplitN(word, "\n", 2)[0]))
	if word == "" || len(word) > 16 || strings.ContainsFunc(word, func(r rune) bool { return r < 'A' || r > 'Z' }) {
		return "QUERY"
	}
	return word
}
