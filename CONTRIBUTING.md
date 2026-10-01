# Contributing

The binding rules are in [docs/CONSTITUTION.md](docs/CONSTITUTION.md); the approved design is in
[docs/architecture](docs/architecture/README.md); decisions are in [docs/adr](docs/adr/README.md).

## Workflow

1. `make setup` (or `scripts\dev.ps1 setup` on Windows): installs the pinned tools and enables the git hooks.
2. Branch from `main`: `git switch -c <type>/<topic>` with type one of `feat`, `fix`, `chore`, `docs`, `refactor`, `test`.
   Direct commits and pushes to `main` are blocked (hooks locally, ruleset on GitHub: [docs/operations/branch-protection.md](docs/operations/branch-protection.md)).
3. Keep PRs small (one slice or backlog item). Run `make ci` (or `scripts\dev.ps1 ci`) before pushing.
4. Open a PR using the template. The required check is `ci-gate`; merge by squash or rebase.
5. Architectural change? Write or amend an ADR **before** the code (Constitution P15).

## Rules that CI enforces

- Formatting and lint (`golangci-lint`), `go vet`, unit tests with the race detector.
- Architecture rules (`internal/archtest`): domain imports only stdlib and `internal/shared`; application never imports adapters or platform; connectors stay isolated; no cloud SDKs.
- Documentation links must resolve.
- `govulncheck`, `gosec`, `gitleaks` (history and working tree). SonarCloud when enabled ([docs/operations/sonarcloud.md](docs/operations/sonarcloud.md)).

## Repository hygiene (this repository is MIT-licensed and may become public)

- Never commit secrets, provider credentials, provider contracts, or licensed datasets. `.env*` and `secrets/` are git-ignored.
- **Fixture provenance:** provider fixtures under `test/fixtures/providers/<name>/` may come only from public documentation or a sandbox whose terms allow redistribution. Record the source and date in a `PROVENANCE.md` next to the fixtures, and remove credentials and personal data.
- Do not commit AI-assistant instruction files (`CLAUDE.md`, `AGENTS.md`, `.claude/`); they are git-ignored.

## Definition of Done

See [docs/architecture/08-engineering-standards.md](docs/architecture/08-engineering-standards.md) section 3.
