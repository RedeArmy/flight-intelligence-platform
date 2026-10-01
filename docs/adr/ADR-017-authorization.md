# ADR-017: Authorization model

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Server-side enforcement with minimal complexity is required (Constitution §37).

## Decision
RBAC on API clients with roles USER, DEVELOPER, OPERATOR, ADMIN, SERVICE; deny by default; resource scoping (alerts belong to a client); privileged operations (provider state, flags) require OPERATOR/ADMIN and are audit-logged. ABAC/policy engine reserved for the AI Tool Gateway (ADR-021) and for later if justified.

## Alternatives considered
- Full ABAC now: premature.
- Ad hoc authorization in handlers: error-prone.

## Consequences
+ Clear and testable.
- Role granularity may need extension (scopes per operation class).

## Rejected options
Client-side/UI-only enforcement.
