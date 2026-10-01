# ADR-006: Hexagonal (ports and adapters) architecture

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
The domain must not depend on PostgreSQL, Redis, HTTP or providers (P1, Constitution §102). Testability without external systems is a requirement.

## Decision
Per context: `domain` (pure), `application` (use cases), `ports` (consumer-defined interfaces), `adapters` (PG, Redis, HTTP, gateway). Composition root only in `cmd/*`; explicit constructor wiring, no DI framework. Dependency rules enforced by lint/architecture test.

## Alternatives considered
- Layered architecture with repositories everywhere: encourages persistence leakage into the domain.
- Active-record style models.

## Consequences
+ Fast deterministic unit tests; replaceable infrastructure.
- More packages/boilerplate; interfaces only at boundaries to limit abstraction bloat.

## Rejected options
Generic repository abstractions; interfaces for everything; reflection-based DI containers.
