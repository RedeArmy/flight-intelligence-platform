# ADR-024: Feature flags with lifecycle

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Safe provider activation, ranking/model experiments and API behavior changes (Constitution §55).

## Decision
Server-side flags stored in PG (cached), each with owner, purpose, created_at and review_by; expiry report; audit on change; deterministic evaluation; provider enablement always flag-gated. Permanent behavior belongs in typed config, not flags.

## Alternatives considered
- Third-party flag SaaS: cost and dependency; revisit if needs grow.
- Env-var toggles only: no audit, requires redeploy.

## Consequences
+ Controlled rollout.
- Flag debt (mitigated by the expiry policy).

## Rejected options
Permanent uncontrolled flags.
