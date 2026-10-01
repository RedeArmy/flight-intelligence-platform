# ADR-011: Immutable historical observations with lineage

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
History is the strategic asset (Constitution §3, §19, §21). Overwrites destroy trust and analytics value (INV-3, INV-4).

## Decision
Observation tables are append-only; the application DB role has INSERT/SELECT only. Corrections are new rows. Each row links provider_request, normalization version, quality score and verification. Retention by policy via privileged jobs only. Idempotent inserts under at-least-once delivery.

## Alternatives considered
- Upsert latest-price tables: loses history.
- Event sourcing everywhere: unnecessary complexity beyond observations and status.

## Consequences
+ Trustworthy analytics and audit.
- Storage growth; partition/archive when measured (R-10).

## Rejected options
Updating or deleting observations in place for cleanup.
