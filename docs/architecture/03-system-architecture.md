# 03 — System Architecture (C4, Deployment, Sequences, Events)

## 1. C1 — System context

```mermaid
flowchart TB
  dev[API integrator / internal apps]
  op[Operator / SRE]
  agent[AI agent - future]
  sys[[Flight Intelligence Platform]]
  prov[(Airlines / NDC / authorized feeds)]
  notif[Notification channels<br/>email / webhook - via abstraction]
  idp[Identity provider - future OIDC]
  sm[Cloud secret manager]
  otel[Observability backend<br/>vendor-neutral via OTLP]

  dev -->|HTTPS REST /v1| sys
  agent -.->|via Tool Gateway| sys
  op -->|ops API / dashboards| sys
  sys -->|provider-specific protocols| prov
  sys --> notif
  sys -.-> idp
  sys --> sm
  sys -->|OTLP| otel
```

## 2. C2 — Containers (initial: 2 deployable binaries + managed data stores)

```mermaid
flowchart TB
  client[API clients] --> edge[CDN / WAF / Load balancer]
  edge --> api[cmd/api<br/>Go HTTP API<br/>stateless, N instances]
  api --> pg[(PostgreSQL<br/>system of record)]
  api --> redis[(Redis<br/>cache / rate limit / locks)]
  api --> pgq[(PG job queue + outbox<br/>same cluster, separate schema)]
  worker[cmd/worker<br/>Go worker pools<br/>Search / Verify / Historical /<br/>Monitoring / Intelligence / Notification] --> pg
  worker --> redis
  worker --> pgq
  api --> gw[Provider Gateway<br/>in-process library]
  worker --> gw
  gw --> airlines[(Airline APIs / NDC)]
  gw --> obj[(Object storage<br/>raw responses, off by default)]
  api --> otel[OTel Collector]
  worker --> otel
  api --> sm[Secret manager]
  worker --> sm
```

Why two binaries: workers scale and fail independently of the request path (backpressure, deploy
cadence) at near-zero extra cost; the Provider Gateway stays an **in-process library** used by both
(no network hop, no extra service). Justification and extraction criteria: ADR-001, ADR-012.

## 3. C3 — Components (inside the monolith)

```mermaid
flowchart LR
  subgraph cmd/api
    http[HTTP server<br/>routing, authn, authz,<br/>rate limit, request id, errors]
  end
  subgraph application
    ss[SearchService]
    vs[VerificationService]
    hq[HistoryQueries]
    st[StatisticsService]
    ops[OpportunityService]
    ms[MonitoringService]
  end
  subgraph domain
    dm[Offer, Itinerary, Money,<br/>Verification, State machines,<br/>Deduplicator, Ranker]
  end
  subgraph ports
    pp[ProviderSearch / ProviderVerify]
    pr[OfferRepository / ObservationRepository]
    pq[JobQueue]
    pc[Cache / RateLimiter]
    pn[Notifier]
    pk[Clock / IDGen]
  end
  subgraph adapters
    pgad[Postgres repos]
    rdad[Redis adapters]
    qad[PG queue adapter]
    gwad[Provider Gateway adapter]
    nad[Notification adapters]
  end
  subgraph provider
    gw[Gateway: registry, routing,<br/>auth, timeouts, retries,<br/>rate limits, breaker, health]
    cn[Connectors: mock, airlineX...]
  end
  http --> ss & vs & hq & st & ops & ms
  ss & vs --> dm
  ss & vs & ms --> pp & pr & pq
  pp -.implemented by.-> gwad --> gw --> cn
  pr -.-> pgad
  pq -.-> qad
  pc -.-> rdad
  pn -.-> nad
```

Dependency rule (enforced in CI): `domain` ← `application` ← `adapters`/`cmd`; `ports` defined by
`application` consumers; `provider/connectors/*` import nothing outside `provider` and `shared`.

## 4. Deployment architecture (TARGET, cloud-neutral — not built yet)

> **Local-first (D1, ADR-025):** today everything runs via Docker Compose on one machine (api, worker, postgres, redis, otel-collector, mock provider). The diagram below is the future deployed shape once a vendor is chosen; nothing in it is implemented or required now.

```mermaid
flowchart TB
  inet((Internet)) --> cdn[CDN + WAF]
  cdn --> lb[L7 Load balancer<br/>TLS termination]
  subgraph vpc[VPC - private subnets, 2+ AZ]
    lb --> apis[API service - container, autoscaled]
    wrk[Worker service - container, autoscaled by queue depth]
    apis --> pgm[(Managed PostgreSQL<br/>multi-AZ, PITR)]
    wrk --> pgm
    apis --> rds[(Managed Redis)]
    wrk --> rds
    wrk --> nat[Egress NAT / proxy<br/>allow-listed provider hosts]
    apis --> nat
  end
  nat --> airl[(Airline APIs)]
  apis & wrk --> obj[(Object storage, encrypted)]
  apis & wrk --> secm[Secret manager - IAM-based]
  apis & wrk --> col[OTel collector -> backend]
```

- Compute: managed container service (no Kubernetes; ADR-025). Environments: dev, staging, prod, each separate account/project/state.
- Egress allow-list for provider hosts mitigates SSRF/exfiltration (ADR-018, security doc).
- Release: CI → staging → smoke → canary/controlled prod → auto-rollback on SLO gate.

## 5. Sequence diagrams

### SD-1 Flight search (and SD-2 provider failure)

```mermaid
sequenceDiagram
  autonumber
  participant C as Client
  participant A as API
  participant S as SearchService
  participant G as Provider Gateway
  participant PA as Provider A
  participant PB as Provider B
  participant H as History (recorder)
  C->>A: POST /v1/flights/search
  A->>A: authn, authz, rate limit, validate
  A->>S: Search(req, deadline=8s)
  S->>S: select providers (flags, coverage, health)
  par bounded concurrency (semaphore)
    S->>G: Search(A, ctx 3s)
    G->>PA: provider request (retry only transient, budget-aware)
    PA-->>G: response
  and
    S->>G: Search(B, ctx 3s)
    G->>PB: provider request
    PB--xG: timeout
    G-->>S: ProviderError{Timeout} (breaker records failure)
  end
  G-->>S: normalized + validated offers (A)
  S->>S: quarantine bad records, dedup, rank
  S-)H: record observations (async, durable)
  S-->>A: results + providerOutcomes[A: ok, B: timeout] + partial=true
  A-->>C: 200 (partial=true)
```

If *all* providers fail: `503 SEARCH_UNAVAILABLE` with per-provider outcomes. If none are applicable: `200` with empty results and `coverage: none`.

### SD-3 Offer verification and SD-4 price change

```mermaid
sequenceDiagram
  participant C as Client
  participant A as API
  participant V as VerificationService
  participant G as Provider Gateway
  participant P as Provider
  participant H as History
  C->>A: POST /v1/flights/{offerId}/verify (Idempotency-Key)
  A->>V: Verify(offerId)
  V->>V: load offer, check not EXPIRED, capability check
  V->>G: Verify(providerOfferRef)
  G->>P: price/availability confirmation
  P-->>G: price 734, inventory ok
  G-->>V: ProviderVerificationResult
  alt price == observed
    V->>V: transition -> VERIFIED (and BOOKABLE if capability confirmed)
  else price differs
    V->>V: transition -> PRICE_CHANGED (observed 589, verified 734)
  else sold out
    V->>V: transition -> SOLD_OUT
  end
  V-)H: append verification observation (immutable, lineage)
  V-->>A: Verification{state, observedPrice, verifiedPrice, validUntil}
  A-->>C: 200
```

### SD-5 Historical observation

```mermaid
sequenceDiagram
  participant S as Search/Verify
  participant Q as Outbox/JobQueue
  participant HW as HistoricalWorker
  participant QC as Quality pipeline
  participant DB as PostgreSQL
  participant OS as Object storage
  S->>Q: enqueue ObservationCaptured (same tx as business write: outbox)
  Q->>HW: deliver (at-least-once)
  HW->>QC: schema + semantic validation, scoring
  alt passes
    HW->>DB: INSERT price/availability observation (idempotent on observation key)
  else suspicious
    HW->>DB: INSERT quarantined_records
  end
  opt raw retention enabled for provider
    HW->>OS: put encrypted raw response ref
  end
```

### SD-6 Scheduled monitoring, SD-7 opportunity detection, SD-8 alert delivery

```mermaid
sequenceDiagram
  participant SCH as MonitoringScheduler
  participant Q as JobQueue
  participant MW as MonitoringWorker
  participant S as SearchService
  participant V as VerificationService
  participant I as Intelligence
  participant AL as AlertEvaluator
  participant N as Notifier
  SCH->>SCH: pick due targets by tier + provider budget
  SCH->>Q: enqueue MonitorRoute(target, run_id)
  Q->>MW: deliver
  MW->>S: Search(route, window)
  S-->>MW: candidate offers
  MW->>V: Verify(top candidates under max price)
  V-->>MW: verified/bookable?
  MW->>I: Evaluate(verified offer, history)
  I-->>MW: opportunity(score, reasons, calc_version) or none
  MW->>AL: evaluate alerts (verified & bookable & price <= max)
  AL->>Q: enqueue Notify(alert, offer, idempotency_key)
  Q->>N: deliver notification
  N-->>Q: ack (retry w/ backoff, DLQ after N)
```

Unverified anomalies stop at MW→V: they are recorded as observations/anomalies, never notified (INV-8).

### SD-9 AI search (Phase 2/3, reserved)

```mermaid
sequenceDiagram
  participant U as User
  participant L as LLM
  participant TG as Tool Gateway
  participant PE as Policy Engine
  participant API as Flight Intelligence API
  U->>L: "cheap Guatemala to Madrid in November"
  L->>TG: tool call search_flights(args)
  TG->>PE: authorize(tool, args, principal)
  PE-->>TG: allow (read-only tool)
  TG->>API: POST /v1/flights/search
  API-->>TG: structured result (observed prices + states)
  TG-->>L: result wrapped as untrusted data
  L-->>U: explanation w/ uncertainty (never invented prices)
```

### SD-10 Future booking flow (reserved; outline only)

`Verified/Bookable OfferID → Order(CREATED) → explicit user confirmation → Payment → Ticketing → Servicing`.
Every step crosses a new bounded context; none exists in Phase 1. LLM tools `create_booking`, `pay` etc.
require out-of-band explicit confirmation (Constitution §42).

## 6. Event catalog (event-ready, not event-driven)

Phase 1 uses an **outbox table + PG job queue** for durable async work; events are internal
integration messages (JSON, versioned `type` + `schema_version`), idempotent consumers keyed by
`event_id`. No external broker (ADR-013, ADR-020).

| Event | Producer | Consumers | Notes |
|-------|----------|-----------|-------|
| `search.completed.v1` | shopping | history, monitoring metrics | includes per-provider outcomes |
| `observation.captured.v1` | shopping/verification | history worker | carries lineage + raw ref |
| `offer.status_changed.v1` | verification | monitoring, history | from→to, reasons |
| `verification.completed.v1` | verification | intelligence, monitoring | |
| `opportunity.detected.v1` | intelligence | monitoring | has reason codes, versions |
| `alert.triggered.v1` | monitoring | notification worker | idempotency key |
| `provider.health_changed.v1` | provider | monitoring, ops | state transitions |
| `quarantine.recorded.v1` | history | ops metrics | drift signal |

Reliability: at-least-once; DLQ after max attempts; replay tooling in ops runbook.
