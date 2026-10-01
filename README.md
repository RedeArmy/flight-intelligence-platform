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

Run the API locally (needs `APP_ENV`; copy `.env.example` to `.env` first):

```bash
make run     # serves on :8080 (public) and 127.0.0.1:8081 (operator)
curl -i http://localhost:8080/healthz
```

### Local database
```bash
make local-secrets   # once: generates ./secrets (git-ignored; never overwrites)
make db-up           # PostgreSQL 17 on 127.0.0.1:5432, password auth for every role
make migrate         # applies migrations as fip_migrator
make run             # the API reads ./secrets and reports /readyz ready once PostgreSQL answers
make test-db         # separate throw-away database on 127.0.0.1:55432 for `make integration`
```
The runtime role (`fip_app`) cannot change the schema or alter the audit log; see [ADR-031](docs/adr/ADR-031-postgresql-access-roles-migrations-secrets.md).
