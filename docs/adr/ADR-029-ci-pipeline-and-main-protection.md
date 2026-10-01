# ADR-029: CI pipeline, main protection and architecture tests

- **Status:** Accepted (2026-10-01)
- **Date:** 2026-10-01
- **Review date:** 2027-04-01 (earlier if a trigger named in Consequences occurs)
- **Deciders:** owner (RedeArmy) with E1 plan approval

## Context
No application code exists yet, but `main` must be protected and every change gated before E1 coding starts (Constitution sections 52, 53). The repository is solo-maintained, private for now, and local-first (no deploy stages).

## Decision
GitHub Actions workflow `ci.yml` runs on every pull request and on pushes to `main` with these jobs: lint, test (race detector), arch, security (govulncheck, gosec), secrets (gitleaks over full history), build, integration (PostgreSQL and Redis service containers; tests arrive from E1-7), and an optional sonar job.

A single aggregate job `ci-gate` is the only required status check, so jobs can change without editing protection. A repository ruleset (`.github/rulesets/protect-main.json`) requires pull requests, `ci-gate`, linear history and resolved conversations, forbids force pushes and deletion, and has no bypass actors. Required approvals are 0 while there is one maintainer. Local git hooks block direct commits and pushes to `main` as a second layer.

Third-party actions are pinned to full commit SHAs, workflow permissions default to read-only, and tools come from the pinned Makefile. Architecture rules are enforced by a Go test (`internal/archtest`, no extra dependency) plus depguard rules in golangci-lint; the same test checks that relative markdown links resolve. SonarCloud analysis runs from CI (not Automatic Analysis) and is switched on by the repository variable `SONAR_ENABLED`.

## Alternatives considered
- Many individually required checks: protection must be edited whenever jobs change.
- CodeQL and GitHub dependency review: need GitHub Advanced Security on private repositories; govulncheck and gosec cover the need for now.
- Third-party architecture linters: an extra dependency for rules a short test expresses.

## Consequences
+ Merges are gated by one stable check, and the rules are code and testable.
- Rulesets on private repositories need a paid GitHub plan (to verify); until then only the local hooks apply.
- Approvals at 0 means GitHub does not enforce review; checks and the PR checklist carry the load. Raise the count when a second maintainer exists.
- archtest allows connectors to import only `internal/provider` and `internal/shared` until the canonical-model package location is decided in E2.

## Rejected options
Direct pushes to `main`; skipping checks for docs-only changes (required checks must always report).
