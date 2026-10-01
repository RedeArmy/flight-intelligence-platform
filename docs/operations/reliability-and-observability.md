# Reliability, SLOs, Observability, DR, Operations

All numeric targets are hypotheses (Constitution §47) to validate by measurement.

## 1. SLOs and SLIs

| SLO | SLI | Target | Window |
|-----|-----|--------|--------|
| API availability | non-5xx / total, excluding client errors (4xx) and *upstream provider* failures that are reported as partial | 99.9% | 30 d |
| Metadata API latency | p95 of `GET /v1/airlines|airports` | < 100 ms | 30 d |
| Historical API latency | p95 of history/statistics | < 500 ms | 30 d |
| Search latency | p95 end-to-end | < 10 s (platform overhead measured separately from provider time) | 30 d |
| Search usefulness | searches returning ≥1 offer when ≥1 healthy applicable provider | ≥ 99% | 30 d |
| Data freshness | monitored targets observed within tier interval | ≥ 95% | 7 d |
| Notification timeliness | alert fired → delivery attempt | p95 < 60 s | 30 d |

Error budget policy: when 50% of monthly budget is consumed, feature releases require SRE approval; at
100%, only reliability/security changes ship until budget recovers.
No external SLA is offered until measured history exists.

## 2. Observability (OpenTelemetry; vendor-neutral OTLP)

Correlation fields on every log/span: `trace_id`, `span_id`, `request_id`, `correlation_id`
(search/monitor run), plus `provider_id`, `api_client_id` (not key), `route`.

### Metrics (Prometheus-style names; units explicit)

| Area | Metrics |
|------|---------|
| API | `http_server_requests_total{route,status}`, `http_server_duration_seconds` (p50/95/99), in-flight, saturation |
| Provider | `provider_requests_total{provider,op,outcome}`, `provider_duration_seconds`, `provider_timeouts_total`, `provider_rate_limited_total`, `provider_circuit_state{provider}`, `provider_health_state` |
| Search | `search_total`, `search_provider_calls`, `search_results`, `search_partial_total`, `search_dedup_ratio`, `search_deadline_exceeded_total` |
| Verification | `verification_total{result}`, `verification_price_change_total`, `verification_sold_out_total`, `verification_bookable_ratio` |
| Data | `observation_freshness_seconds{tier}`, `quality_failures_total{reason}`, `quarantine_total`, `missing_fields_total`, `duplicate_rate` |
| Workers | `queue_depth{queue}`, `job_latency_seconds`, `job_failures_total`, `job_retries_total`, `dlq_depth` |
| Cost | `provider_cost_estimate_total{provider,op}`, cost per search/verification/route (derived) |
| DB/Redis | pool saturation, slow queries, replication lag, Redis errors/latency |

Cardinality rules: no unbounded labels (no offerId, no raw route per series beyond top-N rollups).

### Tracing
Spans: HTTP → application service → gateway → connector → provider call; context propagates into jobs via
job metadata. Sampling: always for errors/slow, probabilistic otherwise (tail sampling at collector).

### Logging
Structured JSON, levels, redaction middleware (SR-09). Retention: 30 days hot. Never log secrets, tokens,
payment data, full PII, provider credentials or entire provider responses.

### Alerts (symptom-based, paging)
SLO burn-rate (fast+slow windows); all-providers-down; breaker open on a provider > N min; queue age >
threshold; DLQ growth; quarantine-rate spike; DB failover/replication lag; certificate/secret expiry.
Every page links to a runbook.

## 3. Reliability patterns

Timeouts everywhere with deadline propagation; retry only transient + budget-aware + jitter; circuit
breakers per provider/operation; bulkheads (per-provider semaphores/worker pools); rate limits; graceful
degradation (partial results, serve stale stats with `dataFreshness` flag); backpressure (bounded
queues, load shedding with 429/503 + `Retry-After`); health checks (liveness cheap, readiness checks
critical deps with short timeout); graceful shutdown (drain in-flight, stop workers cleanly).

Failure mode matrix (resilience tests): provider timeout/500/429/malformed/auth; Redis down (degrade to
no cache + local fallback limiter, fail-open vs fail-closed per feature: rate limit fails *closed-ish*
with conservative local limit; cache fails open); queue/DB down (readiness fails, no data loss since
outbox is transactional); worker crash mid-job (visibility timeout + idempotent handlers).

## 4. Disaster recovery

| Item | Initial design |
|------|----------------|
| Targets | RPO 15 min, RTO 1 h (hypotheses; validate with drills) |
| PostgreSQL | multi-AZ HA + PITR (continuous WAL) + daily snapshot; cross-region snapshot copy (cost-justified later) |
| Redis | treated as rebuildable cache; no restore required; rate-limit counters reset acceptable |
| Object storage | versioning + lifecycle; replication only if raw data is retained |
| IaC | entire env recreatable from Terraform + CI; secrets re-provisioned from secret manager backups |
| Scenarios | AZ loss, region loss, DB corruption/bad migration, credential compromise, provider mass-failure, accidental deletion |
| Drills | restore PITR into scratch env quarterly and measure RTO/RPO; "a backup never restored is not a backup" |
| Runbooks | `docs/operations/runbooks/`: db-restore, provider-disable, key-rotation, queue-drain/replay, rollback, failover |

## 5. Incident management

Severity: **SEV1** platform down/data corruption/security breach; **SEV2** major capability or all providers
down, SLO fast-burn; **SEV3** single provider down, degraded; **SEV4** minor/non-urgent. Roles: incident
commander, comms, ops. Blameless postmortem within 5 business days for SEV1–2; action items tracked in backlog.
On-call rotation defined before production (single-engineer reality: documented escalation + alert routing).

## 6. Operational policies

Feature flags: owner, purpose, created_at, review_by; stale-flag report weekly. Releases: staging →
smoke → canary → prod with automated rollback on health gates. Change log + audit trail for privileged ops.
Capacity review monthly using the model in `docs/architecture/06-capacity-and-cost.md`.

## 7. Local-first scope (D1, ADR-023, ADR-025)
Sections 1-4 describe targets for the future deployed environment. Today: SLO dashboards run against the local
Compose stack (useful for load/resilience tests with mock providers, not as production evidence); DR is scripted
`pg_dump` + WAL archive and `make restore-drill`; managed multi-AZ/PITR, cross-region copy and IaC rebuild apply later.
