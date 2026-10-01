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
- E1-5 (L) HTTP server: routing, middleware (request-id, recover, authn, authz, rate limit, otel), graceful shutdown.
- E1-6 (M) API-key authn: hash storage, prefix lookup, expiry/revoke, constant-time compare; roles.
- E1-7 (M) PostgreSQL platform (pool, tx helper, health) + migration tool + first migrations (reference + api keys).
- E1-8 (S) Redis platform (client, health) + rate limiter adapter (token bucket) + degraded-mode behavior.
- E1-9 (M) OpenTelemetry (traces/metrics/logs) wiring + local collector.
- E1-10 (M) Docker images (non-root/distroless) + Compose (api, worker stub, pg, redis, otel). Compose creates distinct DB roles (migrator, app, readonly) (SR-24).
- E1-11 (L) CI: format, lint (incl. dependency-rule arch test), tests, OpenAPI lint/diff, SAST, SCA, secret, container, IaC scans, SBOM. **PARTIAL 2026-10-01**: ci.yml with lint, workflow lint, race tests, arch, govulncheck, gosec, gitleaks, build, integration skeleton, ci-gate, optional SonarCloud; remaining: OpenAPI lint/diff/drift (S2), container build + scan + SBOM (S6), pin service images by digest
- E1-12 (M) OpenAPI v1 skeleton (health, error schema, auth scheme, airlines/airports) + generated server/validation.
- E1-13 (DEFERRED, D1) Terraform skeleton: blocked until a cloud vendor is chosen. No IaC in the repo yet.
- E1-14 (S) `cmd/worker` skeleton with clean shutdown + `JobQueue` port stub.
- E1-15 (S) Runbook skeletons (rollback, key rotation, provider disable).
- E1-16 (S) Repo hygiene (SR-18): `.gitignore` for env/secrets/keys/local data, pre-commit + CI secret scan, fixture provenance rule in CONTRIBUTING. **DONE 2026-10-01** (.gitignore, gitleaks in CI and pre-commit, CONTRIBUTING fixture rule)
- E1-17 (S) Environment guard (SR-19): startup fails if mock connector or local SecretStore is enabled with a production-like `APP_ENV`.
- E1-18 (M) Operator listener separation (SR-21) and uniform failed-auth handling + failed-auth rate limit (SR-23).

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
- X-2 ASVS adoption ADR (version/level) — before E1 exit.
- X-3 SLO dashboards + burn alerts — E1 baseline, refined per phase.
- X-4 Load-test tooling decision — before E5.
- X-5 Legal/compliance review plan (D7) — before E7 (retention) and again before E12.
