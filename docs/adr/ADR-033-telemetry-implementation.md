# ADR-033: Telemetry implementation: OTLP/HTTP, local providers, untrusted trace context and bounded labels

- **Status:** Accepted (2026-10-01)
- **Date:** 2026-10-01
- **Review date:** 2027-04-01 (earlier when the worker or a second service is instrumented)
- **Deciders:** owner (RedeArmy) with E1 plan approval

## Context
ADR-019 chooses OpenTelemetry with OTLP to a collector and leaves the backend open. Slice S5 implements it for the API. Requirements: SR-09 (nothing sensitive in telemetry), the cardinality rules of `docs/operations/reliability-and-observability.md`, and vendor neutrality (D1).

## Decision
**Transport.** OTLP over HTTP (protobuf) to the collector. The design document named gRPC; HTTP was chosen because it needs only `net/http`, has no long-lived channel to manage, and passes through ordinary proxies. Honest caveat: it does not remove `google.golang.org/grpc` from the module, because the OTLP protobuf types the exporters use import it; the benefit is operational simplicity, not a smaller dependency graph. Switching to gRPC later is one exporter constructor.

**Providers are explicit.** `platform/observability/telemetry` builds a tracer provider and a meter provider and hands them to the code that uses them. Nothing is registered globally, so tests run in parallel with their own providers.

**Off by default, but correlated.** Without `TELEMETRY_OTLP_ENDPOINT` no exporter exists: nothing leaves the process and no connection errors appear. Spans are still created, so every log line carries `trace_id` and `span_id`. The endpoint must be `https` in staging and production and cannot contain credentials. A missing or slow collector never fails startup and never blocks requests; shutdown flushes with a five-second bound.

**Untrusted trace context.** Every public request starts a **new root span**; an inbound `traceparent` is attached as a *link*, not used as the parent. Callers are untrusted: honouring their parent would let them force sampling (cost) and inject their own trace IDs into ours. Operators can still follow a caller's trace through the link.

**Sampling.** The API samples with `ParentBased(TraceIDRatioBased(TELEMETRY_SAMPLE_RATIO))`, default 1 locally. Keeping errors and slow requests while thinning the rest is the collector's job (tail sampling: errors and requests over one second always, the rest by percentage), because only the collector sees the whole trace.

**What is recorded.** HTTP spans are named `METHOD route-template` after routing and carry the method, route template, status code and request ID. They never carry the raw path, query string, headers, body, client address or user agent. 5xx marks the span as an error without a message. Database spans (`db SELECT`) carry the system and the statement's verb only, never SQL text, arguments or error text, and exist only under a traced request, so background statements cannot create one-span traces. Authentication failures are counted by a fixed class (`missing`, `malformed`, `invalid_credentials`, `inactive`), never by reason text.

**Metrics** (catalogue in `docs/operations/telemetry.md`): request count and duration by method, route template and status; in-flight requests; authentication failures by class; rate-limit refusals by limit; connection-pool saturation; readiness state; Go runtime gauges. Requests that match no route share the single label `unmatched`, so scanners cannot create unbounded series. Names translate to the Prometheus names of the design document (`http_server_requests_total`, `http_server_duration_seconds`, ...).

**Own HTTP and pgx instrumentation.** The HTTP middleware and the pgx tracer are about 100 lines each, instead of `otelhttp` and a third-party pgx tracer. The reasons are the two rules above: the span is named after the route only after routing, and the trace context is untrusted. A library would have needed configuration around both and gives less control over what is recorded.

## Amendment (E1 closure, 2026-10-02): SLO rules and the dashboard
The availability SLO is computed in Prometheus from the request counter, with recording rules and multi-window, multi-burn-rate
alerts (a fast burn that pages, a slow burn that opens a ticket) in `deployments/local/prometheus-rules/slo.yml`, and the rules are
unit-tested with promtool (`make observability-check`). Probes and client errors are excluded from the SLI. A Grafana with one
provisioned dashboard shows them (ADR-034). While writing the first rule, verification on the live stack found that the ratio had no
value when there were no failures at all, which is the healthy case; the numerator now falls back to zero and a test asserts it. There is
still no Alertmanager: alerts are visible, not delivered, until a delivery channel is chosen.

## Alternatives considered
- OTLP over gRPC: as the design said; adds channel management, and does not shrink the dependency graph (see above).
- A `/metrics` endpoint on the operator listener: a second path and another route to protect (SR-21); one path through the collector is enough.
- Exporting to stdout when no endpoint is set: noisy and no use for metrics.
- Trusting inbound trace context: lets callers control sampling and trace IDs.
- `otelhttp` and `otelpgx`: less code, less control over recorded attributes.
- Exemplars and request-level high-cardinality attributes: rejected by the cardinality rules.

## Consequences
+ Logs, traces and metrics correlate by trace ID and request ID without a backend decision.
+ A caller cannot inflate telemetry cost or forge trace identity.
+ Telemetry content is reviewable in two small files and covered by tests that assert what must never be exported.
- With `TELEMETRY_SAMPLE_RATIO` below 1 the API drops traces before the collector's tail sampling sees them; keep it at 1 until volume requires otherwise.
- The readiness gauge runs the readiness checks on every metric collection (every 15 seconds by default), which adds one database ping and one Redis ping per interval.
- The SDK also reads standard `OTEL_*` environment variables; the `TELEMETRY_*` keys are the supported configuration.
- Compose does not include the collector, Jaeger and Prometheus until S6; until then the collector is run by hand (see `deployments/local/otel-collector.yaml`).

## Rejected options
Recording SQL text, request bodies or client addresses; labels with unbounded values (client IDs, raw paths, offer IDs); global provider registration; making telemetry export a startup dependency.
