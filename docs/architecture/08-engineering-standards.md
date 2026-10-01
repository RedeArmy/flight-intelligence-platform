# 08 — Engineering Standards: Repository, Coding, DoD, Deprecation, Flags

## 1. Repository structure (as created; changes require ADR or tech-lead approval)

```
cmd/api, cmd/worker             thin mains: config → wire → run → graceful shutdown
internal/<context>/{domain,application,ports,adapters}
    contexts: shopping, verification, history, intelligence, monitoring
internal/provider/{gateway,resilience,connectors/<name>}
internal/platform/{config,database,cache,queue,observability,security,httpserver,featureflags}
internal/shared/{money,errors,clock}     tiny kernel; additions need justification
api/openapi/v1   migrations   test/{integration,contract,e2e,performance,resilience,fixtures/providers}
deployments/{docker,local}   terraform/{modules,environments/*}   scripts   .github/workflows
docs/{product,architecture,adr,api,security,operations,testing,roadmap}
```

Reference data (airlines/airports/routes) starts under `internal/shared` or a small
`internal/reference` package (decision in ADR-007); it must stay read-mostly and dependency-free.
No `pkg/` (nothing is a public library). Go module path: `github.com/RedeArmy/flight-intelligence-platform` (D3).

### Dependency rules (enforced by lint/arch test in CI)

1. `domain` imports: stdlib, `internal/shared/*` only.
2. `application` imports: own `domain`, own `ports`, `internal/shared/*`. Never `adapters`, never `platform`.
3. `adapters` may import own `ports`/`domain` and `internal/platform/*`; not other contexts' internals.
4. Cross-context calls go through the other context's **application service interface** or a port — never its `domain` or `adapters`.
5. `internal/provider/connectors/<x>` imports only `internal/provider/*`, canonical `domain` types via a thin mapping package, `internal/shared`; never other connectors.
6. `cmd/*` is the only composition root (explicit constructor wiring; no DI framework, no global state).

## 2. Coding standards

Idiomatic Go; small packages; explicit over clever; `context.Context` first param on I/O; no globals
except constants; no `init()` side effects; errors wrapped with `%w` and classified (sentinel/typed);
interfaces at architectural boundaries, defined by consumers, small; constructors validate and return
value objects; time and IDs injected via `Clock`/`IDGen` for determinism; bounded concurrency
(errgroup + semaphore), no unbounded goroutines, no arbitrary `time.Sleep` (use timers/contexts);
table-driven tests; `-race` in CI; structured logging via `slog` with redaction; no reflection-heavy
frameworks; dependencies minimal and justified in PR; generated code isolated and marked.

Rejection list = Constitution §102 (business logic in handlers, domain coupled to PG/providers, search
treated as verification, unbounded goroutines, secrets logged, etc.).

## 3. Definition of Done (Constitution §100, checklist form)

- [ ] Requirement + design linked; ADR if architectural
- [ ] Code + unit tests; integration/contract tests where relevant
- [ ] Error handling classified; failure modes tested
- [ ] Security review item completed (threat-model delta if new surface)
- [ ] Logs, metrics, traces added; dashboard/alert if new failure mode
- [ ] OpenAPI + docs updated; compatibility diff clean
- [ ] Migration reviewed (expand/contract, lock impact)
- [ ] Performance considered (budget, load test if hot path)
- [ ] CI green; code review approved
- [ ] Deployed to staging, smoke passed; rollback strategy stated
- [ ] Roadmap/backlog and ADR statuses updated

## 4. Code review standard

Correctness, domain integrity, reliability under dependency failure, security abuse cases, 10×/100×/1000×
load behavior, 12-month maintainability, diagnosability, testability without external systems, API
compatibility, operational/provider cost impact (Constitution §101).

## 5. API and deprecation policy

- Within `/v1`: additive only. Breaking → `/v2`, both supported through the deprecation window.
- Deprecation steps: announce (changelog + docs) → `Deprecation` and `Sunset` headers → migration guide → usage tracking per client → minimum notice (proposed ≥ 6 months for external, ≥ 1 release for internal) → removal on the published date.
- Internal contracts (events, canonical model) follow the same additive rule with `schema_version`.
- Provider connector `normalization_version` bumps are recorded; old observations remain valid and labeled.

## 6. Feature flag policy

Each flag: `key`, `owner`, `purpose`, `created_at`, `review_by` (≤ 90 days default). CI/report fails
or warns on expired flags. Flags cover provider activation/routing, ranking algorithm versions, ML
models, experimental intelligence, API behavior toggles. Evaluation is server-side, deterministic, and
audit-logged on change. Permanent configuration belongs in typed config, not flags.

## 7. Technical debt policy

Any shortcut is recorded in `docs/roadmap/tech-debt.md` (what, why, risk, payoff trigger, owner) in the
same PR that introduces it. Debt without an entry is a review rejection.

## 8. Config standards

Typed config struct, loaded from env/files, validated at startup (fail fast), secrets referenced by name
from the SecretStore port (never inline), documented in `docs/operations/configuration.md` (created in E1).
