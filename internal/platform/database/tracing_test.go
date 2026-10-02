package database

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSQLVerb(t *testing.T) {
	cases := map[string]string{
		"SELECT 1":                              "SELECT",
		"  select * from t":                     "SELECT",
		"INSERT INTO t VALUES ($1)":             "INSERT",
		"\nUPDATE t SET a = 1":                  "UPDATE",
		"-- note\nDELETE FROM t":                "DELETE",
		"/* hint */ SELECT 1":                   "SELECT",
		"-- only a comment":                     "QUERY",
		"/* unterminated SELECT 1":              "QUERY",
		"":                                      "QUERY",
		"   ":                                   "QUERY",
		"1; DROP TABLE users":                   "QUERY",
		"SELECT\nid FROM t":                     "SELECT",
		"WITHAVERYLONGIDENTIFIERTHATISNOTAVERB": "QUERY",
		"(SELECT 1)":                            "QUERY",
	}
	for sql, want := range cases {
		if got := sqlVerb(sql); got != want {
			t.Errorf("sqlVerb(%q) = %q, want %q", sql, got, want)
		}
	}
}

func newTraceRig(t *testing.T) (*queryTracer, *tracetest.SpanRecorder, context.Context) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	parent, span := tp.Tracer("test").Start(context.Background(), "parent")
	t.Cleanup(func() { span.End() })
	return &queryTracer{tracer: tp.Tracer("db")}, rec, parent
}

func TestStatementsUnderATracedRequestGetAClientSpan(t *testing.T) {
	tr, rec, parent := newTraceRig(t)
	ctx := tr.TraceQueryStart(parent, nil, pgx.TraceQueryStartData{SQL: "SELECT secret FROM api_keys WHERE prefix = $1", Args: []any{"abcd1234"}})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	spans := rec.Ended()
	if len(spans) != 1 || spans[0].Name() != "db SELECT" {
		t.Fatalf("spans = %v", spans)
	}
	for _, kv := range spans[0].Attributes() {
		if v := kv.Value.String(); v == "abcd1234" || len(v) > 40 {
			t.Errorf("attribute %s = %q: only the system and verb may be recorded, never SQL text or arguments", kv.Key, v)
		}
	}
	if len(spans[0].Attributes()) != 2 || spans[0].Status().Code == codes.Error {
		t.Fatalf("attributes = %v status = %v", spans[0].Attributes(), spans[0].Status())
	}
}

func TestStatementsWithoutAParentSpanAreNotTraced(t *testing.T) {
	tr, rec, _ := newTraceRig(t)
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	if n := len(rec.Ended()); n != 0 {
		t.Fatalf("%d spans: background statements (metric callbacks, health checks) must not create one-span traces", n)
	}
}

func TestAFailedStatementMarksTheSpanWithoutTheErrorText(t *testing.T) {
	tr, rec, parent := newTraceRig(t)
	ctx := tr.TraceQueryStart(parent, nil, pgx.TraceQueryStartData{SQL: "INSERT INTO t VALUES ($1)"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("duplicate key value (email)=(person@example.com)")})

	s := rec.Ended()[0]
	if s.Status().Code != codes.Error || s.Status().Description != "" || len(s.Events()) != 0 {
		t.Fatalf("status %+v events %v: the error text can hold personal data and must not be recorded", s.Status(), s.Events())
	}
}

func TestPoolConfigInstallsTheTracerOnlyWhenAProviderIsSet(t *testing.T) {
	base := Config{
		Host: "h", Port: 5432, Name: "d", User: "u", SSLMode: SSLDisable, MaxConns: 1, ConnectTimeout: 1,
	}
	pc, err := base.poolConfig()
	if err != nil || pc.ConnConfig.Tracer != nil {
		t.Fatalf("no provider must mean no tracer: %v %v", pc.ConnConfig.Tracer, err)
	}
	base.Tracing = sdktrace.NewTracerProvider()
	pc, err = base.poolConfig()
	if err != nil || pc.ConnConfig.Tracer == nil {
		t.Fatalf("a provider must install the tracer: %v", err)
	}
}
