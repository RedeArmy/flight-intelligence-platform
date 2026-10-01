# ADR-030: Go version and toolchain policy

- **Status:** Accepted (2026-10-01)
- **Date:** 2026-10-01
- **Review date:** 2027-04-01 (earlier if a trigger named in Consequences occurs)
- **Deciders:** owner (RedeArmy) with E1 plan approval

## Context
Constitution section 5: use a currently supported stable Go release and upgrade on the official lifecycle (Go is not an LTS). go.dev lists go1.27.1 as the current stable release (verified 2026-10-01); it is also the installed local version.

## Decision
Pin the language and toolchain version in `go.mod` (`go 1.27.1`) and `.tool-versions`. CI uses `actions/setup-go` with `go-version-file: go.mod`; builds set `GOTOOLCHAIN=local` so the toolchain never changes silently. Upgrade patch releases promptly (security fixes) and minor releases within one month of release, staying within the two newest supported minors of the Go release policy. Each upgrade is a small PR that also bumps pinned tool versions if needed. Dependencies are pinned by `go.sum` and built with `-mod=readonly`.

## Alternatives considered
- Float on the latest release in CI: not reproducible.
- Pin an older minor for stability: leaves the supported window sooner.

## Consequences
+ Reproducible builds and compliance with the supported window.
- Requires a calendar reminder for Go releases (two minors per year); Dependabot covers modules and actions.

## Rejected options
Automatic toolchain downloads (`GOTOOLCHAIN=auto`) in CI or Docker builds.
