# ADR-013: Job queue abstraction backed by PostgreSQL initially

- **Status:** Accepted (D4, 2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Durable async work is needed from E8; Redis is non-authoritative (C8); no broker is justified yet (Constitution §31-§32).

## Decision
Define a `JobQueue` port (Publish, Consume with ack/retry/DLQ semantics). Initial adapter: PostgreSQL `jobs` table using `FOR UPDATE SKIP LOCKED`, run_at scheduling, idempotency key, separate schema/pool. Later targets (SQS, Pub/Sub, Kafka) sit behind the same port when measured need appears (throughput, fan-out, retention).

## Alternatives considered
- Redis streams/lists: faster but durability and ops risk for critical jobs.
- Managed broker now: extra infrastructure and cost without measured need.

## Consequences
+ Transactional enqueue with business writes (outbox).
- PG load; monitored via queue depth/age and pool isolation (R-12).

## Rejected options
Kafka in Phase 1; in-memory-only queue for durable work.
