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

Resource attributes: `service.name` (`api` or `worker`), `service.version`, `deployment.environment.name`. Every metric above is emitted by both processes except `auth.failures` and `rate.limited`, which only the API records; filter by the `job` label in Prometheus.

## Logs

Every log record written while handling a request carries `request_id`, `trace_id` and `span_id`. To follow a request: take the `request_id` from the `X-Request-Id` response header or an error body, find its log lines, and open the trace with the `trace_id` in the tracing backend.

## SLOs, alerts and the dashboard
The availability SLO of `docs/operations/reliability-and-observability.md` is measured from `http_server_requests_total`:
**good** requests (2xx and 3xx) over **valid** requests (2xx, 3xx and 5xx), for the `api` job, with the liveness and readiness
probes excluded (they are not user traffic) and client errors (4xx) counted for neither side. The target is 99.9%, an error budget
of 0.1%. All numbers are hypotheses to validate by measurement.

| What | Where |
|------|-------|
| Recording rules `slo:api_error_ratio:rate5m`, `rate30m`, `rate1h`, `rate6h` | `deployments/local/prometheus-rules/slo.yml` |
| `ApiAvailabilityFastBurn` (page): error ratio above 14.4 times the budget over 5 minutes **and** 1 hour | same file |
| `ApiAvailabilitySlowBurn` (ticket): above 6 times the budget over 30 minutes **and** 6 hours | same file |
| `ServiceNotReady` (page), `ServiceDegraded`, `DatabasePoolSaturated`, `DatabasePoolWaiting`, `AuthenticationFailuresHigh` (tickets) | same file |
| Unit tests of every rule and alert | `deployments/local/prometheus-tests/slo_test.yml`, run by `make observability-check` |
| Dashboard "Flight Intelligence: SLO overview" | `deployments/local/grafana/dashboards/slo-overview.json`, Grafana at `http://127.0.0.1:3000` |

Every alert names its runbook in a `runbook` annotation, and every page links one of `docs/operations/runbooks/`. There is no
Alertmanager in the local stack: firing alerts show in the Prometheus UI (Alerts) and in the dashboard's "Alerts firing" panel.

**Grafana** is provisioned from files (the data source and the dashboards), so it holds no state: edit the JSON, not the dashboard
(the UI refuses to save a provisioned dashboard). Log in as `admin` with the password in `secrets/grafana_admin_password`.
It is published on `127.0.0.1` only and sends nothing out (no usage reports, update checks or plugin downloads).

When a metric or label changes, update the catalogue above, the rules and the dashboard together: an architecture test fails when a
panel or a rule uses a metric that is not in the catalogue, or an alert points to a runbook that does not exist.

## Try it locally

1. Start the collector by hand (see the header of `deployments/local/otel-collector.yaml`; Compose gets it in S6).
2. Run the API with `TELEMETRY_OTLP_ENDPOINT=http://127.0.0.1:4318`.
3. Make some requests, then read `http://127.0.0.1:8889/metrics` for the Prometheus view; the collector's own log shows the traces it kept. With the full local stack (`make dev`), open Grafana at `http://127.0.0.1:3000` for the SLO dashboard and Jaeger at `http://127.0.0.1:16686` for traces.

## Adding telemetry

Add an instrument in `internal/platform/observability/telemetry/metrics.go` with a fixed, documented label vocabulary, add it to the table above, and add a test that asserts its labels. Anything that records request data needs a test that proves a secret or personal value cannot reach it.
