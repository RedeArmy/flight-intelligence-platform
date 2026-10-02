# Telemetry catalogue

What the API emits, under which name and with which labels (ADR-019, ADR-033). Prometheus names are what you see after the
collector translates the OpenTelemetry names: dots become underscores, counters gain `_total`, a unit of `s` becomes `_seconds`.

## Metrics

| OpenTelemetry name | Prometheus name | Type | Labels | Meaning |
|--------------------|-----------------|------|--------|---------|
| `http.server.requests` | `http_server_requests_total` | counter | `http_request_method`, `http_route`, `http_response_status_code` | Requests handled |
| `http.server.duration` | `http_server_duration_seconds` | histogram | same | Request duration; buckets 5 ms to 10 s |
| `http.server.active_requests` | `http_server_active_requests` | up-down counter | none | Requests in flight |
| `auth.failures` | `auth_failures_total` | counter | `reason_class` = `missing`, `malformed`, `invalid_credentials`, `inactive` | Failed authentications |
| `rate.limited` | `rate_limited_total` | counter | `limit` = `ip`, `auth_failure`, `client` | Requests refused by a rate limit |
| `readiness.state` | `readiness_state` | gauge | none | 0 not ready, 1 degraded, 2 ready |
| `db.pool.acquired_connections` | `db_pool_acquired_connections` | gauge | none | Connections in use |
| `db.pool.idle_connections` | `db_pool_idle_connections` | gauge | none | Idle connections |
| `db.pool.max_connections` | `db_pool_max_connections` | gauge | none | Pool size limit |
| `db.pool.empty_acquires` | `db_pool_empty_acquires_total` | counter | none | Acquisitions that waited because every connection was busy (saturation) |
| `go.goroutines` | `go_goroutines` | gauge | none | Goroutines |
| `go.memory.heap_in_use` | `go_memory_heap_in_use_bytes` | gauge | none | Heap in use |
| `go.gc.cycles` | `go_gc_cycles_total` | counter | none | Completed GC cycles |

**Cardinality rules.** `http_route` is the route template (`/v1/whoami`), never the raw path; requests that match no route use the single value `unmatched`. Status codes are a small fixed set. Never add a label with an unbounded value: client or key identifiers, offer IDs, addresses, user agents.

## Traces

| Span | Kind | Attributes |
|------|------|------------|
| `METHOD route-template` (for example `GET /v1/whoami`) | server | `http.request.method`, `http.route`, `http.response.status_code`, `request.id` |
| `db VERB` (for example `db SELECT`) | client | `db.system.name`, `db.operation.name` |

Each public request is a new root trace; a caller's `traceparent` is attached as a link (ADR-033). Spans never carry paths with identifiers, query strings, headers, bodies, SQL text, arguments, client addresses or error messages. Server errors (5xx) mark the span as an error with no message.

Resource attributes: `service.name` (`api`), `service.version`, `deployment.environment.name`.

## Logs

Every log record written while handling a request carries `request_id`, `trace_id` and `span_id`. To follow a request: take the `request_id` from the `X-Request-Id` response header or an error body, find its log lines, and open the trace with the `trace_id` in the tracing backend.

## Try it locally

1. Start the collector by hand (see the header of `deployments/local/otel-collector.yaml`; Compose gets it in S6).
2. Run the API with `TELEMETRY_OTLP_ENDPOINT=http://127.0.0.1:4318`.
3. Make some requests, then read `http://127.0.0.1:8889/metrics` for the Prometheus view; the collector's own log shows the traces it kept.

## Adding telemetry

Add an instrument in `internal/platform/observability/telemetry/metrics.go` with a fixed, documented label vocabulary, add it to the table above, and add a test that asserts its labels. Anything that records request data needs a test that proves a secret or personal value cannot reach it.
