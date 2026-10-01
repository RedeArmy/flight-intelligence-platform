# ADR-019: Observability with OpenTelemetry

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Operability from day one with vendor neutrality (Constitution §45-§46, P7).

## Decision
OpenTelemetry SDK for traces, metrics and logs, exported by OTLP to a collector; the backend is chosen separately (aligned with D1). Correlation fields (trace_id, span_id, request_id, correlation_id). RED metrics per provider; SLO-based paging; structured redacted logs; cardinality guardrails.

## Alternatives considered
- Vendor SDKs directly: lock-in.
- Logs only: insufficient for provider isolation debugging.

## Consequences
+ Portable instrumentation.
- A collector to operate; sampling decisions needed.

## Rejected options
Unstructured logs; high-cardinality labels (offerId).
