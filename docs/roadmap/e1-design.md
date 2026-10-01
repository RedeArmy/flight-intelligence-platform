# E1 — Engineering Foundation: Design Plan

Status: **APPROVED 2026-10-01 (Q1-Q5 answered; see section 12). No code written yet; S0 is next.** Process: Constitution §83 / §111 (design → review → ADR → plan → implement).
Scope source: roadmap E1 and backlog E1-1..E1-18 ([roadmap.md](roadmap.md), [backlog.md](backlog.md)). Deployment is local-only (D1).

## 1. Problem and goal

Produce a production-quality, **local-first** engineering foundation on which E2+ can add domain code without rework:
runnable API and worker skeletons, typed validated config, redacted structured logging, a classified error model, an
OpenAPI-first HTTP server with API-key authn/authz and rate limiting, PostgreSQL + Redis wiring, migrations, OpenTelemetry,
Docker/Compose, a pull-request CI pipeline with security scanning, and enforced architecture boundaries.

**Exit criteria (from roadmap):** `make setup && make dev` works; CI green; `/healthz` and `/readyz` respond; traces visible locally.
Additional exit criteria added by this plan: architecture-boundary test passes; secret-leak test passes; authn/z + rate-limit
integration tests pass; `make restore-drill` exists (ADR-023 amendment); all Definition-of-Done items (08 §3) satisfied.

## 2. Review against AGENTS.md / baseline / ADRs / roadmap

| Check | Result |
|-------|--------|
| Bounded contexts affected | **None** (no domain code). Touches `cmd/*`, `internal/platform/*`, `internal/shared/*`, `api/`, `migrations/`, `deployments/`, `.github/`. Contexts remain empty. |
| Business invariants at risk | None directly. E1 *enables* INV-3 (insert-only roles, SR-24) and sets patterns for INV-5/9. |
| ADR conformance | ADR-001/002/003/004/005/006/012/013/016/017/018/019/023/025 all honored. |
| Constitution tensions found | (a) `Makefile` interface (§72) vs a Windows machine without `make`; (b) `docker compose` required for PG/Redis but Docker is not installed; (c) Go 1.27.1 is installed: pin it, verify it is a supported release (§5) at E1-1. See section 11 (decisions). |
| New API surface | `/healthz`, `/readyz` (unversioned) and one proposed authenticated probe `GET /v1/whoami` (needed to test authn/z/rate limits end to end). Flagged for approval (Q3). |
| Roadmap fit | E1 precedes E2/E3; E1-13 (Terraform) stays deferred (D1). |

## 3. Environment findings (this machine)

| Tool | State | Impact |
|------|-------|--------|
| Go | 1.27.1 windows/amd64 present | OK; pin in `go.mod`/`.tool-versions` |
| Git | 2.56 | OK |
| Docker / Compose | **not installed** | Cannot run PG/Redis/Compose or integration tests locally until installed (Docker Desktop with WSL2 backend) |
| GNU make | **not installed** | Makefile unusable as-is on Windows shell |
| golangci-lint, migrate, oapi-codegen, k6 | not installed | Install via `go install` (pinned versions in `tools.go`/`go.mod` tool directive) or release binaries |
| Python | installed | Optional for scripts; not a project dependency |

Mitigation: ADR-028 defines the dev interface as **Makefile as the canonical contract + identical PowerShell wrappers
(`scripts/dev.ps1 <target>`)** so Windows works without `make`; CI uses `make`. Unit tests need no Docker; integration tests are
behind `//go:build integration` and run locally once Docker exists and always in CI.

## 4. Delivery slices (each = one small PR on its own branch, CI green before merge)

| Slice | Backlog | Content | Depends |
|-------|---------|---------|---------|
| S0 Bootstrap | E1-1, E1-16 | `go.mod` (module `github.com/RedeArmy/flight-intelligence-platform`), Go pin, `.gitattributes` (LF), `.editorconfig`, `.golangci.yml`, Makefile + `scripts/dev.ps1`, `.env.example`, PR template, CONTRIBUTING (fixture provenance rule), pre-commit secret scan config | — |
| S1 Core libs | E1-2, E1-3, E1-4 | `shared/errors`, `shared/clock`, `platform/config` (+ `docs/operations/configuration.md`), `platform/observability/logging` (redaction) | S0 |
| S2 HTTP + OpenAPI | E1-5, E1-12 | `platform/httpserver` (chi, middleware chain, health, graceful shutdown, error mapping), `api/openapi/v1/openapi.yaml`, oapi-codegen wiring, `cmd/api` | S1 |
| S3 Postgres | E1-7 | `platform/database` (pgxpool, tx helper, health), migrations 0001-0003, roles, `make migrate`, `make restore-drill` | S1 |
| S4 Security | E1-6, E1-8, E1-18, E1-17 | API-key authn, RBAC, audit events, Redis rate limiter + local fallback, failed-auth limiter, operator listener, env guard, `GET /v1/whoami` | S2, S3 |
| S5 Observability | E1-9 | OTel traces/metrics/log correlation, collector config | S2 |
| S6 Containers | E1-10 | Dockerfile (distroless, nonroot), Compose (pg, redis, collector, viewers, api, worker), DB role init | S3, S5 |
| S7 Worker skeleton | E1-14 | `cmd/worker`, lifecycle, `platform/queue` port (types only, no adapter until E8) | S1 |
| S8 CI | E1-11 | GitHub Actions: format, lint, arch test, unit, integration (service containers), OpenAPI lint/diff/codegen drift, SAST, govulncheck, dependency review, secret scan, image build + scan, SBOM | S0-S6 (grows as slices land; skeleton first) |
| S9 Ops docs | E1-15 | Runbook skeletons (rollback, key rotation, provider disable, db restore) | S3, S4 |

Order recommendation: S0 → S8-skeleton (so every later PR is gated) → S1 → S2 → S3 → S4 → S5 → S6 → S7 → S9 → finish S8.

## 5. Component design

### 5.1 Configuration (`internal/platform/config`)
- Typed `Config` struct grouped (App, HTTP, Operator, Postgres, Redis, Auth, Telemetry, RateLimit, Providers-placeholder). Loaded from environment (12-factor) with an optional `.env` file for local dev; **stdlib-only loader** (no config framework).
- `Validate()` at startup; any invalid mandatory value → exit non-zero with a clear, secret-free message (fail fast).
- `APP_ENV` ∈ `local|test|staging|production`. SR-19 guard: `local`-only features (local SecretStore adapter, mock connector) refuse to start when `APP_ENV` is `staging|production`.
- Secrets never live in `Config` as plain strings: type `Secret` (redacts in `String()`, `MarshalJSON`, `slog.LogValue`). Values come through the `SecretStore` port (ADR-018): local adapter reads env/files; `Config` holds *references*.
- Docs: every key listed in `docs/operations/configuration.md` (name, type, default, required, secret?). A test fails if a key is undocumented.

### 5.2 Logging (`platform/observability/logging`)
- `log/slog`, JSON handler. Fields: `time, level, msg, service, env, version, trace_id, span_id, request_id, correlation_id`.
- Redaction `ReplaceAttr`: denylist keys (`authorization, api_key, password, token, secret, cookie, set-cookie, credential*`) and `Secret` type; pattern scan for the API-key format `fip_<prefix>_<secret>`. **Leakage test** logs known secrets through every code path touched in E1 and asserts absence (E1-3 acceptance).
- Access log middleware logs method, route template (not raw path), status, duration, bytes, client id (never key), request id.

### 5.3 Error model (`internal/shared/errors`)
```
type Kind int  // Invalid, Unauthenticated, Forbidden, NotFound, Conflict, RateLimited, Unavailable, Timeout, Internal
type Error struct { Kind Kind; Code string; Message string; Details map[string]any; Cause error }
```
- Constructors per kind, `Wrap`, `errors.Is/As` support, `Code` stable (matches API error catalog). `Message` is client-safe; `Cause` is logged, never returned.
- Mapping to HTTP lives in `httpserver` only (Kind→status, body per api-design §4). Unknown error → 500 `INTERNAL`, no detail leak. Domain/application never import `net/http`.

### 5.4 HTTP server (`platform/httpserver`, `cmd/api`)
- `net/http` + chi. **Two listeners**: public (`:8080`) and operator/internal (`:8081`: `/metrics`, `/debug` disabled by default, operator routes later) — SR-21.
- Middleware order: `Recover → RequestID → OTel → AccessLog → SecurityHeaders → BodyLimit → AuthN → RateLimit → AuthZ(route permission) → Handler`. Health routes bypass AuthN.
- Server timeouts set (ReadHeader/Read/Write/Idle), `MaxBytesReader`, strict JSON decoding (`DisallowUnknownFields`), request deadline propagated via context.
- Generated handlers from OpenAPI (oapi-codegen strict-server for chi); handlers only translate and call application services (none yet).
- `/healthz` liveness (no deps). `/readyz` readiness: Postgres required (ping, 1 s timeout); Redis **optional** (reported `degraded`, still ready) per ADR-004; draining flag flips to not-ready on shutdown.
- Graceful shutdown: `signal.NotifyContext(SIGINT, SIGTERM)` → mark not-ready → stop accepting → `Shutdown(ctx, 25 s)` → close pools → flush telemetry.

### 5.5 OpenAPI (`api/openapi/v1/openapi.yaml`)
- OpenAPI 3.1; components: `Error` schema (api-design §4), `ApiKeyAuth` (HTTP bearer), shared params (`Idempotency-Key`, pagination), `Whoami` response. Paths: `GET /v1/whoami` only (plus health documented outside `/v1`).
- CI: spec lint, codegen drift check (`git diff --exit-code` after `make generate`), breaking-change diff vs `main` (oasdiff-class).

### 5.6 PostgreSQL (`platform/database`, `migrations/`)
- `pgxpool` with explicit limits (`MaxConns`, `MinConns`, `MaxConnLifetime`, health check period), per-workload pools (api, worker) configured separately; `WithTx(ctx, fn)` helper with retry-on-serialization hook; `Ping` for readiness.
- Migrations: **golang-migrate**, plain SQL, `NNNN_name.up.sql/.down.sql`, expand/contract rules in `migrations/README.md`. Never auto-run in production; `make migrate` locally; CI applies from zero and verifies mixed-version with the previous release's schema.
- Roles (SR-24, created by Compose init script, not by app migrations): `fip_migrator` (DDL), `fip_app` (DML, no DDL, **no UPDATE/DELETE on append-only tables**), `fip_readonly`.
- E1 migrations: `0001_api_clients_keys` (api_clients, api_keys), `0002_audit_events` (insert-only audit log for key lifecycle/privileged ops), `0003_schema_meta` (version/compat marker). IDs UUIDv7 generated in Go (`IDGen` port) — no DB extension needed. Reference data tables are **E2**, not E1.

### 5.7 Authentication and authorization (`platform/security`)
- Key format `fip_<prefix>_<secret>`: prefix 8 chars (public id), secret 32 random bytes base64url (256-bit). Stored: `prefix` (unique), `secret_hmac = HMAC-SHA-256(pepper, secret)`, `expires_at`, `revoked_at`, `created_at`, `last_used_at` (batched async update).
  Rationale (ADR-027): secret is high entropy, so a keyed fast MAC is appropriate (slow KDF unnecessary and a DoS lever); pepper comes from the SecretStore; supports rotation by overlapping validity.
- Verify: lookup by prefix → constant-time compare → check expiry/revocation. Unknown prefix, wrong secret, expired, revoked → **identical** `401 UNAUTHENTICATED` and equalized work (SR-23); failures counted per IP + prefix with limiter.
- RBAC: static role→permission table in code (roles USER, DEVELOPER, OPERATOR, ADMIN, SERVICE); routes declare required permission in the OpenAPI extension (`x-permission`) and are **deny-by-default** (a route without a declared permission fails the startup/route-audit test).
- Audit: key created/revoked/used-after-expiry, authn failures (aggregated), privileged calls → `audit_events` (insert-only). Key issue/revoke needs an admin path: a small separate `cmd/keyctl` CLI. It **adds a binary**, so it is proposed in S4 and gated on Q5.

### 5.7b Rate limiting
- Port `RateLimiter` (consumer-defined in `httpserver`); adapters: Redis token bucket via Lua (atomic) and an in-memory fallback used when Redis is unhealthy (conservative per-instance limit; ADR-004).
- Dimensions: IP (pre-auth), api client + operation class (`read|search|verify` classes defined now, used by later phases). Response `429 RATE_LIMITED` + `Retry-After` + `RateLimit-*` headers.

### 5.8 Redis (`platform/cache`)
- Client wrapper with timeouts (short, 50-100 ms), health, circuit-ish fail-open for cache and conservative fail-over for limiter. No business data stored (ADR-004).

### 5.9 Observability (`platform/observability`)
- OpenTelemetry SDK: traces + metrics via OTLP/gRPC to collector; resource attrs (`service.name`, `service.version`, `deployment.environment`); parent-based sampling (always-on locally); `otelhttp` on server, pgx tracing, runtime metrics; logs correlated by `trace_id`.
- E1 metrics: `http_server_requests_total`, `http_server_duration_seconds`, in-flight, `auth_failures_total{reason_class}`, `rate_limited_total`, `db_pool_*`, `readiness_state`. Cardinality rules from ops doc §2.
- Collector config in `deployments/local/otel-collector.yaml`; viewers: Jaeger (traces) + Prometheus (metrics) in Compose (ADR-028; Q2).

### 5.10 Containers and Compose (`deployments/`, `Dockerfile`)
- Multi-stage Dockerfile: Go build with `-trimpath`, static binary, `gcr.io/distroless/static:nonroot` (digest-pinned), read-only root FS, no shell, healthcheck via the binary's own `healthcheck` subcommand.
- `deployments/local/docker-compose.yml`: `postgres` (init script creates roles/db), `redis`, `otel-collector`, `jaeger`, `prometheus`, `api`, `worker`; named volumes; healthchecks; `profiles` so `make dev` can start infra only (run Go locally) or full stack. Secrets from gitignored `.env`.
- `make restore-drill`: dump, restore into scratch DB, run integrity query, print elapsed time (ADR-023).

### 5.11 Worker skeleton (`cmd/worker`, `platform/queue`)
- Lifecycle identical to API (config, logging, telemetry, graceful shutdown), tiny health listener, no jobs yet. `platform/queue` defines `Job`, `Handler`, `JobQueue` (ADR-013) — types only, **no adapter** until E8 (avoid unjustified abstraction).

### 5.12 Architecture enforcement
- `internal/archtest` (Go test, uses `go list -deps -json`): asserts dependency rules from 08 §1 (domain imports only stdlib+shared; application never imports adapters/platform; connectors isolated; `cmd/*` only composition root; no cloud SDK imports (D1)). Backed by `depguard` in golangci-lint for fast editor feedback. No extra dependency.

### 5.13 CI (`.github/workflows`)
- `ci.yml` on PR/push to main: jobs `format-lint`, `unit` (`-race`, coverage), `integration` (service containers PG+Redis, `-tags integration`), `openapi` (lint, drift, breaking diff), `security` (govulncheck, CodeQL/gosec, gitleaks, dependency-review), `container` (build, Trivy scan, SBOM via Syft as artifact).
- Hardening: `permissions: contents: read` default, per-job elevation only where needed; third-party actions pinned to full commit SHA; no secrets needed for PRs; concurrency groups; caching of Go module/build.
- Deploy/IaC/signing stages **not created** (D1).

## 6. API changes
| Endpoint | Auth | Notes |
|----------|------|-------|
| `GET /healthz`, `GET /readyz` | none | unversioned, operational |
| `GET /metrics` | none, **operator listener only** | Prometheus scrape; not on public port |
| `GET /v1/whoami` | any valid key | returns `clientId`, `role`, `rateLimit` info; used by tests/operators (proposed, Q3) |

No breaking-change risk (nothing prior). Error schema and security scheme defined once here become the `/v1` contract.

## 7. Database changes
`api_clients`, `api_keys`, `audit_events` (+ schema meta). All expand-only; no destructive migration. `audit_events` and (later) observation tables: INSERT-only for `fip_app`. Down migrations provided for 0001-0003 for local dev only.

## 8. Security implications (SR mapping)
Implemented in E1: SR-01 (TLS expectation documented; local plain HTTP), SR-02, SR-03, SR-04 (edge validation via OpenAPI), SR-05/SR-18 (SecretStore, gitignore, scanning), SR-08 (limits), SR-09 (redaction), SR-12 (audit), SR-13 (CI scans), SR-15/SR-24 (roles), SR-17 (container), SR-19, SR-20 (idempotency scoping contract defined; storage in first use), SR-21, SR-23. Deferred with owner phase: SR-06/SR-07/SR-10/SR-22 (provider-facing, E3), SR-14 (E11), SR-16 (infra, post-vendor).
New abuse cases tested: key enumeration, replay of expired/revoked keys, operator-route exposure on public port, log leakage, SQL role privilege (app cannot UPDATE/DELETE audit), oversize/unknown-field bodies.

## 9. Testing strategy
| Layer | E1 tests |
|-------|----------|
| Unit | config validation matrix, `Secret` redaction, error kind→HTTP mapping, key generate/verify/HMAC, RBAC table, route-permission audit, limiter math (fake clock) |
| Architecture | `archtest` dependency rules |
| Integration (`-tags integration`) | PG: migrations from zero + down, role privileges, pool health; Redis: limiter script, fallback on outage; API: authn matrix, rate limit 429 + headers, readiness degraded/ready/not-ready, graceful shutdown drain |
| Security | log-leak test, uniform 401 (status/body/timing-bounded), operator route not on public port |
| Contract | handlers vs OpenAPI (generated types + request/response validation middleware in tests) |
| Resilience (minimal) | Redis down, PG down → `/readyz` behavior; SIGTERM during in-flight request |
| Smoke | Compose up → `/healthz`, `/readyz`, `/v1/whoami`, trace appears in Jaeger |
Determinism: injected `Clock`/`IDGen`; no `time.Sleep`. Coverage: informational, but 100% branches on key verification and error mapping.

## 10. Risks
| ID | Risk | Mitigation |
|----|------|-----------|
| E1-R1 | Docker/make unavailable locally blocks integration work | ADR-028 PowerShell wrappers; install Docker Desktop (Q1); CI runs integration regardless |
| E1-R2 | Over-building (frameworks, premature abstractions) | Stdlib-first; queue port types-only; no adapter until E8; ADR for each added dependency |
| E1-R3 | OTel Go API churn / dependency weight | Pin versions, isolate in `platform/observability`, one upgrade path |
| E1-R4 | Auth timing/enumeration side channels | Equalized work, constant-time compare, tests, SR-23 |
| E1-R5 | CRLF/LF churn on Windows (already warned by git) | `.gitattributes` `* text=auto eol=lf` in S0 |
| E1-R6 | Go 1.27.1 support status unverified here | Verify against go.dev release policy at E1-1; pin; ADR-030 |
| E1-R7 | CI actions supply-chain | SHA pinning, minimal permissions, Dependabot for actions |
| E1-R8 | Public MIT repo accidental secret commit | gitignore + gitleaks pre-commit and CI (SR-18) |
| E1-R9 | Scope creep into E2 (reference data/airlines) | Hard rule: no domain tables/endpoints in E1 |

## 11. Required ADRs (to write with S0, before the related slice)
| ADR | Topic | Slice |
|-----|-------|-------|
| ADR-026 | Error model and HTTP conventions (kinds, codes, middleware order, listeners, readiness semantics) | S1/S2 |
| ADR-027 | API key format, HMAC-SHA-256 + pepper hashing, rotation, audit | S4 |
| ADR-028 | Local developer environment: Makefile + PowerShell wrappers, Compose, observability viewers (Jaeger + Prometheus), roles init | S0/S6 |
| ADR-029 | CI pipeline composition, action pinning, architecture test approach (archtest + depguard) | S8 |
| ADR-030 | Go version and toolchain policy (pinning, upgrade cadence, tools via `tool` directive) | S0 |
Existing ADRs updated by decision notes only (003 migrations, 005 codegen) — already recorded under D5.

## 12. Decisions needed from the owner
| ID | Question | Recommendation |
|----|----------|----------------|
| Q1 | Install Docker Desktop (WSL2) and GNU make now? **Done (verified 2026-10-01).** | Docker Desktop: yes. Make: optional (PowerShell wrappers exist). |
| Q2 | Local observability viewers | Jaeger + Prometheus (small, standard). Alternative: single Grafana LGTM image (heavier, nicer UX). |
| Q3 | Add `GET /v1/whoami` as the authenticated probe? | Yes; smallest surface that exercises authn, authz, rate limit, audit. |
| Q4 | Pin Go 1.27.1 as the toolchain version? | Yes, after verifying it is a currently supported release. |
| Q5 | Add a `cmd/keyctl` admin CLI for issuing/revoking API keys? | Yes, a few hundred lines; otherwise keys must be inserted by SQL, which is error-prone and unaudited. |

### Decisions recorded (2026-10-01)
| ID | Decision | Effect |
|----|----------|--------|
| Q1 | Docker Desktop (WSL2) and GNU make installed and verified on the dev machine | Go 1.27.1, make 4.4.1, Docker 29.8.0 + Compose v5.5.1 working; unblocks integration tests and Compose |
| Q2 | **Jaeger + Prometheus** as local viewers | ADR-028; collector exports traces to Jaeger, metrics to Prometheus; logs to stdout |
| Q3 | **Add `GET /v1/whoami`** | In OpenAPI skeleton (S2); used by authn/authz/rate-limit/audit tests (S4) |
| Q4 | **Pin Go 1.27.1** after verifying support status against the official release policy | ADR-030; `go.mod` `go` + `toolchain` directives, `.tool-versions`, `GOTOOLCHAIN=local` in CI/Docker. If 1.27.1 is not a supported release, report to owner before pinning |
| Q5 | **Add `cmd/keyctl`** (issue/list/revoke API keys) | Third binary in S4; key shown once at creation; every action written to `audit_events` |

## 13. Definition of Done for E1
Slice-level: 08 §3 checklist per PR. Phase-level: exit criteria in section 1; all ADR-026..030 Accepted; docs updated (`README` quickstart, configuration.md, runbook skeletons, OpenAPI); backlog statuses updated; rollback = revert PR (no data migration beyond local dev); retrospective note added to `docs/roadmap/`.
