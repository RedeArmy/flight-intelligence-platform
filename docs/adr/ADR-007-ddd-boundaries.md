# ADR-007: Domain-Driven Design boundaries and ubiquitous language

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Clear ownership and language prevent a flight god-model and prepare extraction (Constitution §7, §8).

## Decision
Contexts: shopping, verification, history, intelligence, monitoring; provider as supporting anticorruption layer; reference data as a tiny shared kernel (location `internal/shared` vs `internal/reference` finalized at E2). Reserved contexts have no code. Cross-context interaction only via application-service interfaces/ports; no cross-context table reads. The glossary in 02-domain is normative.

## Alternatives considered
- Single flights package: faster initially but erodes boundaries.
- Fine-grained contexts per capability: premature.

## Consequences
+ Language consistency, extraction path.
- Some DTO duplication accepted over coupling.

## Rejected options
Shared ORM entities across contexts.
