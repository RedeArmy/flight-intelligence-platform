# ADR-004: Redis for ephemeral state only

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Need low-latency cache, rate limiting, short-lived locks and an idempotency fast path; Redis must not become authoritative (Constitution §19, contradiction C8).

## Decision
Managed Redis for response/reference caching, rate-limit counters, distributed locks (TTL, never the sole correctness guard), idempotency fast path (durable record in PG), worker coordination hints. Loss of Redis must degrade performance, not correctness. The rate limiter has a conservative local fallback.

## Alternatives considered
- No Redis, PG only: possible at small scale but loads the OLTP DB on hot paths.
- In-memory per-instance only: incorrect across instances.

## Consequences
+ Fast hot paths.
- Another dependency to run and monitor. DB constraints remain the backstop for any lock-guarded invariant.

## Rejected options
Redis as queue of record, primary store, or for observation persistence.
