# 01 — Product and Requirements

## 1. Product vision

An independent **flight data and intelligence infrastructure**: acquire flight information through
authorized airline channels, normalize it into a canonical model, verify availability and price, keep an
immutable history of observations, analyze pricing behavior, detect opportunities, and expose all of it
through a stable versioned API. The chatbot, dashboards and any future travel agency are *consumers* of
this core, never its foundation (Constitution §3, §115).

Strategic asset: **flight data + verification + historical intelligence + provider independence.**

## 2. Scope

### In scope for the engineering baseline (Phase 1 product scope)
Search, normalization, verification, historical observations, route statistics, basic deterministic
opportunity detection, adaptive monitoring and alerts, metadata (airlines/airports), operability
(observability, security, DR). Machine-to-machine API consumers only.

### Deliberately designed-for but not built
Identity, customers, orders, payments, ticketing, servicing, hotels, cars, insurance, activities, ML,
LLM agent, marketplace, multi-tenancy. Only *boundaries* are preserved (see 02-domain §3).

## 3. Non-goals (Phase 1)

Kubernetes, Kafka, service mesh, microservices, multi-tenancy, payments, ticketing, hotel booking,
travel-agency operations, end-user mobile/web app, LLM agent, ML forecasting, scraping as the primary
acquisition strategy, currency conversion, loyalty/miles, any claim of "guaranteed price".

## 4. Personas

| ID | Persona | Needs |
|----|---------|-------|
| P1 | **API integrator / developer** (internal now, B2B later) | Stable `/v1`, clear errors, OpenAPI, sandbox with mock providers |
| P2 | **Platform operator / SRE** | Provider health, circuit state, queue depth, runbooks, safe feature flags |
| P3 | **Provider integration engineer** | Documented adapter contract, fixtures, contract tests, onboarding checklist |
| P4 | **Pricing analyst / product owner** | Route statistics, history, opportunity reasons, data lineage |
| P5 | **Alert owner** (API client acting for an end user) | Conditions that fire only when verified |
| P6 | **AI agent (future consumer)** | Typed tools, least-privilege policy, structured uncertainty |
| P7 | **Security/compliance reviewer** | Threat model, audit trail, retention, secrets hygiene |

## 5. Use cases

| ID | Use case | Primary persona |
|----|----------|-----------------|
| UC-1 | Search flights origin/destination/dates/pax/cabin, receive deduplicated ranked offers with partial-failure info | P1 |
| UC-2 | Retrieve a previously returned offer with its current status | P1 |
| UC-3 | Verify an offer (existence, inventory, fare, price, bookability where supported) and get an explicit state | P1 |
| UC-4 | Query route price history and availability history | P1, P4 |
| UC-5 | Query route statistics (avg/median/min/max/percentiles/volatility/trend) | P4 |
| UC-6 | List verified opportunities with reason codes and calculation version | P4, P1 |
| UC-7 | Create an alert (route, dates, max price, condition=bookable) and be notified only when satisfied | P5 |
| UC-8 | Operator disables/enables a provider; sees degraded provider and open circuits | P2 |
| UC-9 | Onboard a new provider without touching the domain | P3 |
| UC-10 | Trace where a price came from, when observed, how normalized, when verified (lineage) | P4, P7 |
| UC-11 | (Future) AI agent searches/explains/creates alerts through the Tool Gateway | P6 |
| UC-12 | (Future) Booking flow: verified offer → order → payment → ticket | — |

## 6. Functional requirements

Priority: **M** must (Phase 1), **S** should, **F** future (boundary only).

| ID | Requirement | Pri | Eng. phase |
|----|-------------|-----|-----------|
| FR-01 | Search API fan-out to enabled providers with bounded concurrency, per-provider and overall deadlines | M | E5 |
| FR-02 | Partial results: failure/timeout of some providers must not fail the search; response lists per-provider outcome | M | E5 |
| FR-03 | Normalize every provider response to the canonical model through schema + semantic validation + quality scoring | M | E3/E4 |
| FR-04 | Quarantine suspicious records instead of silently accepting or dropping | M | E4/E7 |
| FR-05 | Deduplicate by canonical itinerary/offer fingerprint, not flight number; keep provider offer IDs separate from internal OfferID | M | E5 |
| FR-06 | Deterministic, explainable ranking (inputs and version recorded) | M | E5 |
| FR-07 | Offer state machine with explicit OBSERVED/AVAILABLE/VERIFIED/BOOKABLE and failure states; append-only transition log | M | E2/E6 |
| FR-08 | Verify endpoint: availability, fare, price, bookability; reports price delta between observed and verified | M | E6 |
| FR-09 | Persist immutable price and availability observations with lineage (provider, request, normalization version, raw ref, verification) | M | E7 |
| FR-10 | Historical queries (price/availability) with pagination and time-range filters | M | E7 |
| FR-11 | Route statistics (deterministic): avg, median, min, max, percentiles, stddev/volatility, trend | M | E9 |
| FR-12 | Opportunity detection with persisted score, features, reason codes, calculation version; only verified candidates surface as opportunities | M | E9 |
| FR-13 | Provider framework: registry, capabilities, config, health, timeout, retry, circuit breaker, rate limit, error mapping, feature flag | M | E3 |
| FR-14 | Monitoring: scheduled route searches by adaptive tier (LOW/NORMAL/HIGH/CRITICAL) | M | E8 |
| FR-15 | Alerts: create/list; evaluate on verified+bookable only when condition demands; idempotent notifications behind an abstraction | M | E8 |
| FR-16 | Airlines and airports metadata APIs | M | E2 |
| FR-17 | API key authentication, role-based authorization, per-operation/per-key rate limits | M | E1 |
| FR-18 | Consistent error model with request IDs; no internal leakage | M | E1 |
| FR-19 | OpenAPI contract generated/validated in CI; `/v1` compatibility check | M | E1 |
| FR-20 | Health, readiness, liveness, graceful shutdown | M | E1 |
| FR-21 | Provider onboarding kit: fixtures, contract-test harness, mock provider for local dev | M | E3 |
| FR-22 | Feature flags with owner/purpose/expiry for provider activation, routing, ranking | S | E3 |
| FR-23 | Raw provider response retention in object storage, off by default, only where contract permits | S | E7 |
| FR-24 | Usage metering hooks per API client (no billing) | S | E8+ |
| FR-25 | AI Tool Gateway + policy engine | F | E11 |
| FR-26 | Orders/payments/ticketing/servicing | F | E12+ |

## 7. Non-functional requirements (hypotheses to validate by measurement)

| ID | Category | Target | Validation |
|----|----------|--------|-----------|
| NFR-01 | API availability | 99.9% monthly | SLO dashboards, error budget |
| NFR-02 | Metadata API latency | p95 < 100 ms | load test |
| NFR-03 | Historical API latency | p95 < 500 ms at agreed data volume | load test with realistic cardinality |
| NFR-04 | Search latency | p95 < 10 s; overall deadline ~8 s; provider timeout ~3 s | provider latency measured separately |
| NFR-05 | Provider isolation | one provider outage cannot reduce other providers' success | resilience tests |
| NFR-06 | Durability | RPO 15 min, RTO 1 h (initial) | restore drills |
| NFR-07 | Security | OWASP ASVS (version/level pinned at adoption), no secrets in repo/logs | CI scans, review |
| NFR-08 | Observability | Every request traced; RED metrics per provider; structured logs | dashboards |
| NFR-09 | Compatibility | No breaking change within `/v1`; mixed-version deploys safe | API diff in CI, expand/contract migrations |
| NFR-10 | Data integrity | Observations immutable; every price has lineage | DB constraints + tests |
| NFR-11 | Cost | Cost per search/verification/route-month is measured and reportable | cost metrics |
| NFR-12 | Operability | New engineer: clone → run → test in documented steps | `make setup && make dev` |

Search p95 note: a 3 s provider timeout with retries cannot fit inside an 8 s budget unless retries are
deadline-aware. See contradiction C5 in [open-questions.md](open-questions.md).

## 8. Business invariants (must hold in code, schema, and API)

- **INV-1** A search result is never bookable by itself. Only a successful verification can raise state.
- **INV-2** Observed price ≠ verified price ≠ bookable price. They are distinct fields/states, never merged.
- **INV-3** Historical observations are append-only. Corrections are new rows referencing the old.
- **INV-4** Every persisted observation carries lineage (source, provider, request, observed_at, normalization version, quality result).
- **INV-5** No provider-specific type crosses the provider boundary into domain/application code.
- **INV-6** Provider offer IDs are never used as internal OfferIDs.
- **INV-7** User/API-facing price claims are phrased by state ("observed at X, verified at Y"). No "flight costs X" unless state supports it.
- **INV-8** An unverified anomaly is never alerted as a confirmed deal.
- **INV-9** A provider failure degrades results; it never fails the platform.
- **INV-10** The LLM layer never reaches providers or databases directly, and never performs high-impact actions without explicit confirmation.
- **INV-11** Money is an integer minor-unit amount plus ISO 4217 currency; never floating point; never summed across currencies.
- **INV-12** Bookable can only be asserted when the provider capability to confirm it exists and was exercised; otherwise the maximum reachable state is VERIFIED.

## 9. Assumptions

| ID | Assumption | Risk if false |
|----|-----------|---------------|
| A-1 | At least one authorized provider/airline channel will be obtainable for the reference integration | No real data; fall back to mock-only until contracted |
| A-2 | Providers expose a search-like and a price/availability-confirmation operation (names/semantics vary) | Verification limited to VERIFIED, not BOOKABLE (INV-12) |
| A-3 | Initial load is low (tens of thousands of searches/day at most) | Revisit queue/DB choices only on measurement |
| A-4 | Provider terms allow storing derived price observations; raw responses only where stated | Raw retention stays off |
| A-5 | A single cloud region with multi-AZ managed services meets 99.9% | Needs multi-region (cost/complexity) |
| A-6 | Single tenant at first; `tenant_id` may be added later by migration | Late tenancy retrofit cost |
| A-7 | Callers are trusted API clients, not anonymous public traffic | Needs WAF/abuse controls tightened |
| A-8 | Team is small; operational simplicity beats theoretical scalability | — |

## 10. Constraints

- Language Go (currently supported stable release; upgrade on the official lifecycle).
- PostgreSQL is the system of record; Redis never authoritative.
- Terraform for infra (DEFERRED, D1: local-only until a vendor is chosen); GitHub Actions for CI (deploy stages deferred); OpenTelemetry for telemetry; Docker for packaging.
- Modular monolith first; microservice extraction only with measurable justification + ADR.
- Authorized connectivity only; no uncontrolled scraping as primary strategy.
- Regulatory (e.g. Guatemala travel-agency rules) are `UNKNOWN`; legal review precedes any commercial phase (E13).
- Cloud vendor intentionally not chosen (D1): everything runs locally via Docker Compose; no cloud SDKs, no Terraform yet. Items below that mention managed cloud services describe the *future deployed* target.

## 11. Success metrics

| Metric | Initial target | Notes |
|--------|---------------|-------|
| Search success rate (≥1 provider result when ≥1 provider healthy) | ≥ 99% | measured excluding no-coverage routes |
| Verification correctness: verified price equals provider price at verification time | 100% (by construction) | audited via lineage |
| Observed→verified price drift distribution | tracked | feeds trust in opportunities |
| Data freshness per monitored route vs tier | ≥ 95% within tier interval | |
| Quarantine rate | tracked, alert on spike | proxy for provider schema drift |
| Cost per search / per verification / per monitored route-month | tracked from first provider | |
| Time to onboard a new provider | trend down; no domain changes required | |
| Opportunity precision (verified & bookable at notify time) | ≥ 95% | |

## 12. Risk register

L = likelihood, I = impact (H/M/L).

| ID | Risk | L | I | Mitigation | Owner |
|----|------|---|---|-----------|-------|
| R-1 | No authorized provider access obtained | M | H | Mock providers drive E1–E3; early commercial/partner track in parallel | Product |
| R-2 | Provider T&Cs forbid storing prices/raw data | M | H | Legal review per provider; retention flags per provider; raw off by default | Eng+Legal |
| R-3 | Provider rate limits/costs make monitoring uneconomical | H | M | Adaptive tiers, cost metrics, per-provider budgets, cache | Eng |
| R-4 | Observed prices misleading users (search ≠ bookable) | H | H | INV-1/2/7, explicit states, wording rules in API docs | Eng |
| R-5 | Provider schema drift breaks normalization silently | H | M | Schema validation, quarantine, contract tests, drift alerts | Eng |
| R-6 | Prompt injection via provider text reaching LLM | M | H | Treat as untrusted; sanitize; Tool Gateway + policy (E11) | Sec |
| R-7 | Credential leak (provider/API keys) | L | H | SecretStore port (local adapter now), scanning, short-lived creds, rotation runbook | Sec |
| R-8 | Over-engineering delays value | M | M | Non-goals, NOW/NEXT/LATER, ADR justification for infra | Tech lead |
| R-9 | Under-engineering of boundaries causes rewrite | M | H | Import rules enforced by CI, ports/adapters, ADR review | Tech lead |
| R-10 | Historical table growth degrades queries | M | M | Index by real queries; measure; partition only on evidence | Eng |
| R-11 | Single-region/single-DB outage exceeds SLO | M | M | Multi-AZ, PITR backups, restore drills, documented RTO | SRE |
| R-12 | Queue on Postgres contends with OLTP | L | M | Separate schema/pool, measured; swap behind `JobQueue` | Eng |
| R-13 | Regulatory exposure when moving to ticketing | M | H | Legal gate before E12/E13; no code assumptions | Legal |
| R-14 | Price-forecast overclaiming | M | M | Always intervals + calibration + model versioning | Data |
| R-15 | Dependency/supply-chain compromise | M | H | Pinning, SBOM, scanning, provenance | Sec |
