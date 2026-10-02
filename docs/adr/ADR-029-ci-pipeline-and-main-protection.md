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

## Decision note (E1 S3, 2026-10-01): migration checks
The pipeline gains a `migrations` job, required through `ci-gate`. It proves that migrations already merged are never
edited, renamed or deleted (a Go command, `scripts/migrationcheck`, run from the merge base so it works on Windows and in
CI), that the policy tests pass, and, against the digest-pinned PostgreSQL image, that each migration applies and reverts
on its own, that `up-down-up` leaves the same schema without residue, and that the resulting schema and privileges equal
a committed snapshot. See `docs/operations/migrations-ci.md`.

## Decision note (E1 S8, 2026-10-02): container checks, dependency review and CodeQL
Three jobs join `ci-gate`, and the caches are enabled.
- **`images`** lints the Dockerfile (hadolint), validates Compose, builds the three images, scans each with Trivy and fails on
  HIGH or CRITICAL vulnerabilities that have a fix, and uploads an SPDX SBOM per image (Syft) as an artifact. Every step is a
  `make` target and every tool a container image pinned by tag and digest, so local runs and CI agree. The first scan found a
  HIGH vulnerability in `google.golang.org/grpc` (an indirect dependency of the OpenTelemetry exporters) that `govulncheck`
  did not report because the vulnerable code is not called; it was fixed by updating the module.
- **`dependency-review`** blocks pull requests that add or update a dependency with a HIGH or CRITICAL vulnerability. It runs on
  pull requests only.
- **`codeql`** adds CodeQL (`security-extended`) next to gosec and SonarCloud, and is skipped for fork pull requests because it
  needs `security-events: write`.
- `ci-gate` treats `sonar`, `dependency-review` and `codeql` as optional only in the sense of *skipped*: a failure or a
  cancellation of any job still blocks the merge, and a skipped required job (`images` and the others) blocks it too.
- Third-party actions are pinned by commit SHA (CodeQL's release tag is annotated, so the commit behind the tag was pinned,
  not the tag object). `setup-go` caches modules and builds keyed on `go.sum`.
- `dependency-review` needs the repository's **Dependency graph** enabled and `codeql` needs CodeQL's *default setup* off; both
  are repository settings that a workflow cannot change, so they are listed as prerequisites in the operations document.
See `docs/operations/container-ci.md`.
