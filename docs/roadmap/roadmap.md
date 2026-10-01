# Technical Roadmap (NOW / NEXT / LATER / FUTURE)

Engineering phases E0–E14 (resolves C1). Do not implement future complexity because it is anticipated.

## NOW
| Phase | Outcome | Exit criteria |
|-------|---------|---------------|
| **E0 Architecture baseline** | This baseline + 25 ADRs + backlog | Baseline approved (open-questions §C) |

## NEXT
| Phase | Outcome | Exit criteria |
|-------|---------|---------------|
| **E1 Engineering foundation** | Go skeleton, typed config, slog+redaction, error model, HTTP server, PG/Redis wiring, Docker/Compose, OTel, health, graceful shutdown, CI + scanning (deploy stages deferred, D1), migrations, OpenAPI, API-key authn/z, rate-limit skeleton; local-only, no Terraform | `make setup && make dev` works; CI green; `/healthz` `/readyz`; traces visible locally |
| **E2 Domain foundation** | Value objects, offer/itinerary/segment, offer state machine, reference data (airlines/airports/routes) + metadata APIs | 100% branch coverage on state machine/money/fingerprints; `GET /v1/airlines|airports` meeting NFR-02 locally |
| **E3 Provider framework** | Interfaces, registry, config, capabilities, gateway (timeouts/retry/breaker/limits/errors), health, flags, mock connector, fixture + contract harness | Resilience tests pass vs mock; onboarding doc validated by dry-run |

## LATER
| Phase | Outcome |
|-------|---------|
| **E4 First provider integration** (BLOCKED: D2 = mock only for now) | Auth, search, parsing, mapping, validation, metrics, fixtures, contract/integration tests; reference implementation |
| **E5 Search engine** | Orchestration, bounded concurrency, partial failure, normalize→validate→dedup→rank, `POST /v1/flights/search`, observation recorder port |
| **E6 Verification** | Verify flow + states (PRICE_CHANGED/SOLD_OUT/EXPIRED/PROVIDER_ERROR), capability-gated bookability, `POST …/verify` |
| **E7 Historical data** | Durable observations, lineage, quality pipeline + quarantine, history APIs, retention jobs, raw store (opt-in) |
| **E8 Monitoring** | Scheduler, PG job queue/workers, adaptive tiers, alerts, evaluation, notification abstraction, idempotency |
| **E9 Deterministic intelligence** | Statistics, trend/volatility, opportunity rules (+ reason codes/versions), `GET /v1/routes/*/statistics`, `GET /v1/opportunities` |

## FUTURE
| Phase | Outcome | Gate |
|-------|---------|------|
| E10 ML (anomaly → classification → forecast → demand → monitoring optimization) | Versioned, evaluated models | Sufficient data volume + drift monitoring plan |
| E11 AI flight agent | Tool Gateway + policy engine + tools | Threat model (T12/T13) red-team passed |
| E12 Marketplace | Offers, orders, booking, payment, ticketing | Legal/commercial gate (D7), provider booking capabilities |
| E13 Digital travel agency | Agency ops, customers, servicing, tax/consumer protection | Guatemala regulatory review (unknown) |
| E14 AI travel platform | Multi-modal planning, hotels, activities, insurance | — |

## Cross-cutting tracks (parallel, not phases)
- **Provider/commercial access track** (starts now): obtain authorized channel(s), contract terms, sandbox, retention rights (R-1, R-2).
- **Security track**: threat-model upkeep, ASVS adoption ADR, key rotation drills.
- **Ops track**: SLO dashboards, runbooks, DR restore drill (first drill before E7 ships to prod).
- **Cost track**: unit-cost metrics from E4.

## Re-planning rule
Roadmap reviewed at the end of each phase; items move NEXT→NOW only with an updated backlog and any needed ADR.
