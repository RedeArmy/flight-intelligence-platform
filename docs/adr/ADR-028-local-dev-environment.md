# ADR-028: Local developer environment and tooling

- **Status:** Accepted (2026-10-01)
- **Date:** 2026-10-01
- **Review date:** 2027-04-01 (earlier if a trigger named in Consequences occurs)
- **Deciders:** owner (RedeArmy) with E1 plan approval

## Context
Deployment is local-only (D1). The development machine is Windows 11 with Go, Docker Desktop (WSL2) and GNU make; make is not guaranteed on every Windows setup. Constitution section 72 requires make targets. Pinned, reproducible tools matter more than convenience.

## Decision
The `Makefile` is the canonical developer and CI interface. `scripts/dev.ps1` mirrors every target for Windows machines without make (both are kept in sync; CI uses the Makefile). Recipes stay shell-agnostic. Tools (golangci-lint, govulncheck, gosec, gitleaks) are pinned by version in the Makefile and installed with `go install` into `./bin` (git-ignored). Git hooks live in `.githooks` and are enabled by `make hooks`. Local infrastructure will be Docker Compose (PostgreSQL, Redis, OpenTelemetry collector, Jaeger for traces, Prometheus for metrics; decision Q2), added in slice S6. Line endings are LF (`.gitattributes`).

## Alternatives considered
- Task or just as the runner: another tool to install and a departure from the constitution.
- PowerShell only: not usable in CI.
- WSL-only development: adds a second environment and slower file access.

## Consequences
+ One contract for humans and CI, and it works on Windows.
- Two entry points must be kept equal (a CI check comparing target lists is a candidate for a later slice).
- The gitleaks module still declares the old `github.com/zricethezav/gitleaks/v8` path, so it is installed from that path until upstream fixes it. Recheck when upgrading tools.

## Rejected options
Unpinned `latest` tools; committing tool binaries.
