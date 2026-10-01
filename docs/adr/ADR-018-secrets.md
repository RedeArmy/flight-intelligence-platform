# ADR-018: Secrets management and service identity

- **Status:** Amended (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Provider credentials and API secrets are high-value (R-7, T3). Constitution §38-§39 forbid secrets in git and logs.

## Decision
A cloud secret manager is the only secret store; workloads use IAM workload identity (no static cloud keys); runtime fetch with caching and rotation support; Terraform references secrets by name, never values; CI uses OIDC federation. Egress allow-list for provider hosts. Secret scanning in CI and pre-commit. Rotation runbook and drills.

## Alternatives considered
- Env vars baked into images: leak-prone.
- Self-hosted vault now: operational burden.

## Consequences
+ Auditable, least privilege.
- Cold-start fetch latency (cache); dependency on secret manager availability (cache plus graceful degradation).

## Rejected options
Committing encrypted secrets as the primary mechanism; sharing credentials across providers.

## Amendment (D1, 2026-09-30)
No cloud vendor is chosen. All secrets are accessed through a `SecretStore` port. The local adapter reads
gitignored env files / mounted files (never committed; `.env*` and `secrets/` in `.gitignore`). A cloud
secret-manager adapter, workload identity and CI OIDC federation are added when a vendor is chosen (follow-up ADR).
Unchanged: no secrets in git/images/logs/API responses, per-provider credential isolation, egress allow-list,
secret scanning in CI and pre-commit, rotation runbook.
