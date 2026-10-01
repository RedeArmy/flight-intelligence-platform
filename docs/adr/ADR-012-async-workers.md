# ADR-012: Asynchronous worker model

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Monitoring, historical writes, intelligence and notifications are provider-bound background work and must not affect API latency (Constitution §30).

## Decision
Logical workers (Search, Verification, Historical, Monitoring, Intelligence, Notification) run as bounded worker pools inside `cmd/worker`. At-least-once delivery, idempotent handlers, visibility timeout, retry with backoff, DLQ, graceful shutdown. Concurrency limits per queue and per provider.

## Alternatives considered
- One service per worker: premature.
- Run in the API process: couples failure and latency domains.

## Consequences
+ Isolation without microservice overhead.
- Requires idempotency discipline and queue observability.

## Rejected options
Unbounded goroutine fan-out; fire-and-forget without durability for important work.
