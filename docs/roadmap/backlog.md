# Engineering Backlog

Format: `ID — item (acceptance gist)`. Size: S ≤ 1d, M ≤ 3d, L ≤ 1w (rough). Dependencies noted.
Phase order follows roadmap. Items for E4+ are intentionally coarse until their phase starts.

## E0 — Baseline
- E0-1 (M) Review + resolve open-questions D1–D4, contradictions C1–C15.
- E0-2 (S) Move ADRs Proposed → Accepted/Amended; set review dates.
- E0-3 (S) PR template, CODEOWNERS, issue templates (ADR required? security? migration?).
- E0-4 (S) Branch protection settings documented/applied.

## E1 — Engineering foundation
- E1-1 (S) `go.mod`, Go version pinned (D3), `Makefile` targets, tool versions file. **DONE 2026-10-01** (go.mod, .tool-versions, Makefile, scripts/dev.ps1, .gitattributes, .editorconfig, .golangci.yml)
- E1-2 (M) Typed config + validation + fail-fast; `docs/operations/configuration.md`. **DONE 2026-10-01** (S1: platform/config, .env.example, docs/operations/configuration.md)
- E1-3 (M) Logging (slog) + redaction middleware + tests proving no secret leakage. **DONE 2026-10-01** (S1: platform/observability/logging + shared/secret, leak test)
- E1-4 (M) Error model package (typed/sentinel, API mapping, `requestId`). **DONE 2026-10-01** (S1: shared/errors; HTTP mapping arrives with S2)
- E1-5 (L) HTTP server: routing, middleware (request-id, recover, authn, authz, rate limit, otel), graceful shutdown. **DONE 2026-10-02 (S2, S4, S4b, S5)**: chi router and middleware chain, error mapping, probes, graceful shutdown, API-key authn/authz, rate limiting, OpenTelemetry.
- E1-6 (M) API-key authn: hash storage, prefix lookup, expiry/revoke, constant-time compare; roles. **DONE 2026-10-02 (S4)**: HMAC-SHA-256 with pepper, prefix lookup, expiry and revocation, constant-time compare, roles, `cmd/keyctl`, audit; ADR-027.
- E1-7 (M) PostgreSQL platform (pool, tx helper, health) + migration tool + first migrations (reference + api keys). **DONE 2026-10-01 (S3)**: platform/database (pgxpool, tx retry, readiness, error classification), migrations 0001-0002 + cmd/migrate, roles incl. fip_admin, privilege matrix tests, ADR-031.
- E1-8 (S) Redis platform (client, health) + rate limiter adapter (token bucket) + degraded-mode behavior. **DONE 2026-10-02 (S4b)**: `platform/cache`, token-bucket limiter on Redis with a local fallback, degraded readiness; ADR-032.
- E1-9 (M) OpenTelemetry (traces/metrics/logs) wiring + local collector. **DONE 2026-10-02 (S5, closure)**: traces, metrics and log correlation, local collector, Jaeger, Prometheus, SLO rules and a Grafana dashboard; ADR-033.
- E1-10 (M) Docker images (non-root/distroless) + Compose (api, worker stub, pg, redis, otel). Compose creates distinct DB roles (migrator, app, readonly) (SR-24). **DONE 2026-10-02 (S6, S7)**: distroless non-root images (api, worker, tools), Compose with PostgreSQL roles, Redis, collector, Jaeger, Prometheus, Grafana, migrations, api and worker; ADR-034.
- E1-11 (L) CI: format, lint (incl. dependency-rule arch test), tests, OpenAPI lint/diff, SAST, SCA, secret, container, IaC scans, SBOM. **DONE 2026-10-02 (S0 to S8)**: lint, race tests, architecture test, OpenAPI lint/drift/breaking diff, govulncheck, gosec, gitleaks, migrations, integration, SonarCloud, container lint/scan/SBOM, dependency review, CodeQL; no IaC scan because there is no IaC (D1); ADR-029.
- E1-12 (M) OpenAPI v1 skeleton (health, error schema, auth scheme, airlines/airports) + generated server/validation. **DONE 2026-10-01 (S2)**: OpenAPI 3.0.3 contract, oapi-codegen strict server, drift and breaking-change checks in CI, contract-vs-routes-vs-policies audit.
- E1-13 (DEFERRED, D1) Terraform skeleton: blocked until a cloud vendor is chosen. No IaC in the repo yet.
- E1-14 (S) `cmd/worker` skeleton with clean shutdown + `JobQueue` port stub. **DONE 2026-10-02 (S7)**: `cmd/worker` with clean shutdown and health listener, shared startup package, `platform/queue` contract (types and rules, no adapter); ADR-035.
- E1-15 (S) Runbook skeletons (rollback, key rotation, provider disable). **DONE 2026-10-02 (S9)**: runbooks for rollback, key rotation and database restore were run on the local stack; `provider-disable` is a skeleton (no provider yet).
- E1-16 (S) Repo hygiene (SR-18): `.gitignore` for env/secrets/keys/local data, pre-commit + CI secret scan, fixture provenance rule in CONTRIBUTING. **DONE 2026-10-01** (.gitignore, gitleaks in CI and pre-commit, CONTRIBUTING fixture rule)
- E1-17 (S) Environment guard (SR-19): startup fails if mock connector or local SecretStore is enabled with a production-like `APP_ENV`. **DONE 2026-10-02 (S3)** for the secret store (the local store refuses production-like environments); the guard for the mock connector arrives with the connector (E3).
- E1-18 (M) Operator listener separation (SR-21) and uniform failed-auth handling + failed-auth rate limit (SR-23). **DONE 2026-10-02 (S2, S4, S4b)**: separate operator listener, one answer for every authentication failure, failed-authentication throttling.

## E2 — Domain foundation
- E2-1 (M) Value objects (Money, AirportCode, AirlineCode, FlightNumber, DateRange, PassengerCount, CabinClass, IDs) + tests/property tests.
- E2-2 (M) Itinerary/segment/offer aggregates + invariants + fingerprints (itinerary vs offer).
- E2-3 (M) Offer state machine + transition log, exhaustive transition tests.
- E2-4 (S) Verification value model (4 tri-state facts, observed vs verified price).
- E2-5 (M) Reference data: schema, seed pipeline (source of IATA/ICAO data decided; licensing check), repos.
- E2-6 (S) `GET /v1/airlines`, `GET /v1/airports`, route resolve helper; ETag caching; perf check NFR-02.
- E2-7 (S) Invariant→test traceability doc.

## E3 — Provider framework
- E3-1 (M) Provider interfaces + capability descriptor + registry.
- E3-2 (M) Provider config/credentials via secret manager abstraction.
- E3-3 (L) Gateway: timeout/deadline propagation, retry (transient only, jitter), rate limiter, bulkhead.
- E3-4 (M) Circuit breaker (per provider/op) + health state machine + metrics.
- E3-5 (M) Error taxonomy + mapping + tests.
- E3-6 (M) Mock connector with latency/failure injection + fixtures set.
- E3-7 (M) Contract test harness + golden mapping files.
- E3-8 (S) Feature flags (owner/purpose/expiry) + provider activation.
- E3-9 (S) `provider_requests` recording + lineage ids.
- E3-10 (S) Provider onboarding guide dry-run with mock.
- E3-11 (M) Outbound HTTP client with dial-time IP validation, no cross-host redirects, size and time caps (SR-22) + SSRF tests.

## E4–E9 (coarse; refine at phase start)
- E4 (BLOCKED, D2: mock only for now): provider selection, contract/legal sign-off, adapter, mapping, fixtures, contract/integration tests, metrics, runbook.
- E5: SearchService, bounded concurrency, partial-failure outcomes, dedup, ranker v1 (recorded), `ObservationRecorder` port, search API, load test.
- E6: VerificationService, capability gating, price-change handling, verify API + idempotency, TTL/expiry job.
- E7: observation tables + insert-only grants, quality pipeline, quarantine, history APIs, retention jobs, raw store (opt-in), first DR restore drill.
- E8: PG queue + outbox, workers, scheduler tiers, alerts API, evaluation, notification abstraction + first adapter, idempotency, DLQ tooling.
- E9: statistics engine, trend/volatility, opportunity rules + persistence (reason codes/versions), statistics/opportunities APIs, outlier candidate flow.

## Cross-cutting
- X-1 Provider/commercial access track (D2, R-1, R-2).
- X-2 ASVS adoption ADR (version/level) — before E1 exit. **DONE 2026-10-02**: ASVS 5.0.0, Level 2 target, Level 1 gate before E2 exits (ADR-036, `docs/security/asvs-coverage.md`).
- X-3 SLO dashboards + burn alerts — E1 baseline, refined per phase. **DONE 2026-10-02 as a baseline**: availability SLO rules and burn-rate alerts with unit tests, one Grafana dashboard; refined per phase.
- X-4 Load-test tooling decision — before E5.
- X-5 Legal/compliance review plan (D7) — before E7 (retention) and again before E12.
