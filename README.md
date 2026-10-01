# Flight Intelligence Platform

Flight data and intelligence infrastructure: acquire, normalize, verify, store history, analyze, and expose via a stable API.

**Status:** Phase 0, architecture baseline. No application code yet.

Start here: [docs/CONSTITUTION.md](docs/CONSTITUTION.md) · [docs/adr](docs/adr) · [docs/architecture](docs/architecture)

## Layout
| Path | Purpose |
|---|---|
| `cmd/api`, `cmd/worker` | Deployable entrypoints (thin `main`) |
| `internal/<context>` | Bounded contexts: shopping, verification, history, intelligence, monitoring (each: domain, application, ports, adapters) |
| `internal/provider` | Provider gateway, resilience, per-airline connectors |
| `internal/platform` | Config, DB, cache, queue, observability, security, HTTP, feature flags |
| `internal/shared` | Small cross-cutting value types (money, errors, clock) |
| `api/openapi` | Versioned OpenAPI contracts (`v1`) |
| `migrations` | Expand/contract-safe SQL migrations |
| `test/` | integration, contract, e2e, performance, resilience, provider fixtures |
| `deployments`, `terraform` | Docker/local compose; IaC per environment |
| `docs/` | product, architecture (C4), ADRs, API, security, operations, testing, roadmap |
| `.github/workflows` | CI/CD |

## Quickstart (developers)
Prerequisites: Go (see `.tool-versions`), GNU make (or `scripts\dev.ps1` on Windows), Docker Desktop (from slice S6).

```bash
make setup   # install pinned tools into ./bin and enable git hooks
make ci      # everything CI runs: vet, lint, workflow lint, tests, architecture rules, security scans, build
```

`main` is protected: work on a branch and open a pull request ([CONTRIBUTING.md](CONTRIBUTING.md), [branch protection](docs/operations/branch-protection.md), [SonarCloud](docs/operations/sonarcloud.md)).
