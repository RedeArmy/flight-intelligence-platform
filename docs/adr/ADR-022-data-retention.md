# ADR-022: Data retention and raw data policy

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Retention must be explicit, legal and minimal (Constitution §23, §50-§51). Provider terms are UNKNOWN until contracts exist.

## Decision
Raw provider responses: off by default; per-provider opt-in with encryption, access control and short retention per contract. Observations and verifications: long-term. Quarantine 90 days, jobs/outbox 14 days, app logs 30 days, audit at least 1 year (proposed numbers pending D7). Retention jobs run under a privileged role; deletions are logged. No PII collected in Phase 1.

## Alternatives considered
- Store everything forever: legal and cost risk.
- Store nothing raw: weaker debugging; accepted as the default.

## Consequences
+ Compliance-ready posture.
- Debugging relies on lineage and fixtures rather than raw payloads by default.

## Rejected options
Logging full provider responses.
