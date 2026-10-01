# ADR-001: Modular monolith with two deployables

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Small team, uncertain load, high need for strong boundaries (Constitution §4, P12, P13). Provider-bound workloads (monitoring) have a different failure and scale profile than the request/response API.

## Decision
One Go module/repo. Bounded contexts as internal packages with enforced dependency rules. Two binaries: `cmd/api` and `cmd/worker`, sharing code. The Provider Gateway is an in-process library. Extraction criteria (each needs an ADR with measurements): independent scaling, independent deploy cadence, different availability/SLO, different data lifecycle, different security boundary, separate ownership, resource isolation.

## Alternatives considered
- Microservices per context: rejected (operational cost, distributed failure modes, no measured need).
- Single binary: viable but couples worker load to API latency.
- Serverless functions: cold starts, harder local dev, harder bounded-concurrency control.

## Consequences
+ Fast local dev, atomic transactions, simple ops. + Boundaries preserved for later extraction.
- Discipline required: boundary erosion is the main risk (mitigated by architecture lint in CI).
- Shared DB until extraction (schema-per-context ownership convention).

## Rejected options
Kubernetes, service mesh or event bus at start; shared mutable global state across contexts.
