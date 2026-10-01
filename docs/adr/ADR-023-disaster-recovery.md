# ADR-023: Disaster recovery strategy

- **Status:** Amended (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Explicit RPO/RTO and tested restore are required (Constitution §49).

## Decision
Targets RPO 15 min and RTO 1 h (hypotheses). Managed PG multi-AZ + PITR + snapshots; Redis rebuildable; object storage versioned; whole environment from Terraform. Quarterly restore drills measuring real RTO/RPO; runbooks in docs/operations/runbooks. Cross-region copy added when cost-justified.

## Alternatives considered
- Multi-region active/active now: cost and complexity unjustified.
- Backups without drills: unverified.

## Consequences
+ Known recovery path.
- Drill cost; targets may need revision after the first drill.

## Rejected options
Untested backups; manual undocumented recovery.

## Amendment (D1, 2026-09-30)
Local-only for now. RPO 15 min / RTO 1 h remain the targets for the future deployed environment. Locally:
scripted `pg_dump` plus WAL archiving to a local volume, and a `make restore-drill` target that restores into a
scratch database, runs integrity checks and reports elapsed time. Managed multi-AZ + PITR, cross-region copies and
infrastructure rebuild-from-IaC apply once a vendor is chosen.
