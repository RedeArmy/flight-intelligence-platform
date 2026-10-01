# ADR-020: Event-ready design with transactional outbox

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Reliable async integration is needed without adopting an event-driven architecture everywhere (Constitution §32).

## Decision
An outbox table is written in the same transaction as state changes; a relay publishes to the `JobQueue`; events are JSON with `type`, `schema_version` and `event_id`; consumers are idempotent; retry/DLQ. Event catalog in 03-system-architecture. An external broker is introduced only via ADR with measured need.

## Alternatives considered
- Dual writes (DB plus queue): inconsistency risk.
- Full event sourcing: unnecessary.

## Consequences
+ No lost or phantom events.
- Relay lag; operational monitoring of outbox age.

## Rejected options
Publishing events outside the transaction boundary.
