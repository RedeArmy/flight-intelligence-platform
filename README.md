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
make test-db         # throw-away PostgreSQL (55432) and Redis (56379) for `make integration`
```
The runtime role (`fip_app`) cannot change the schema or alter the audit log; see [ADR-031](docs/adr/ADR-031-postgresql-access-roles-migrations-secrets.md).

### Rate limiting
Protected routes are limited per address, per failed authentication and per client ([ADR-032](docs/adr/ADR-032-rate-limiting.md)); a limited call gets `429` with `Retry-After`. Set `REDIS_ADDR` (and `REDIS_TLS=false` for a local Redis) to share counters between instances; without it limits apply per instance. Defaults and keys are in [configuration](docs/operations/configuration.md).

### Observability
Logs carry `request_id`, `trace_id` and `span_id`. To export traces and metrics set `TELEMETRY_OTLP_ENDPOINT` (for example `http://127.0.0.1:4318`) and run the collector from `deployments/local/otel-collector.yaml`; what is emitted is listed in [telemetry](docs/operations/telemetry.md) ([ADR-033](docs/adr/ADR-033-telemetry-implementation.md)).

### API keys
Protected routes (such as `GET /v1/whoami`) need `Authorization: Bearer <key>`. Keys are issued by the operator tool, which connects as `fip_admin` and prints the token once ([ADR-027](docs/adr/ADR-027-api-key-format-hashing-rotation.md)):
```bash
go run ./cmd/keyctl client create -name my-app -role DEVELOPER
go run ./cmd/keyctl key issue -client my-app          # 90-day key; the token is shown once
go run ./cmd/keyctl key list
go run ./cmd/keyctl key revoke -prefix <prefix>
```
