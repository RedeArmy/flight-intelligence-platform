# E1 — Engineering Foundation: closure review

Date: 2026-10-02. Scope: slices S0 to S9 of [e1-design.md](e1-design.md), plus the closure work of this review (ASVS adoption, SLO rules
and dashboard, backlog and status updates). Pull requests #1 to #15 are merged on `main`.

**Verdict, for the owner to confirm:** the exit criteria are met and demonstrated. The items that are not met are listed in section 5 with the
reason and the trigger that reopens them; none blocks starting E2. This document is a proposal for sign-off, not the sign-off.

## 1. Exit criteria

Verified on 2026-10-02 in a **fresh clone of `main`** (`4198427`) from GitHub, with its own Compose project, not in the working tree.

| Criterion | Result | Evidence |
|-----------|--------|----------|
| `make setup && make dev` works | **Met** | `make setup` installed the tools and the hooks in 102 s; `make dev` started PostgreSQL, Redis, the collector, Jaeger and Prometheus, all healthy, in 17 s (images were already cached on this machine; the first pull is slower) |
| CI green | **Met** | every pull request #1 to #15 merged through the required check `ci-gate`; `make ci` (vet, lint, workflow lint, unit tests, architecture tests, govulncheck, gosec, gitleaks, build) passes in the clean clone in 126 s |
| `/healthz` and `/readyz` respond | **Met** | `{"status":"ok"}` with 200, and `{"checks":{"postgres":"ok","redis":"ok"},"status":"ready"}` with 200, from the API started on the host |
| Traces visible locally | **Met** | Jaeger lists the `api` service with the spans `GET /v1/whoami`, `GET /healthz`, `GET /readyz`, `db SELECT` and `db UPDATE`; request log lines carry the `trace_id`; Prometheus holds `http_server_requests_total` |
| Architecture-boundary test passes | **Met** | `internal/archtest` (dependency rules, cloud-SDK ban, documentation links, runbook and observability contracts) |
| Secret-leak test passes | **Met** | logging leak tests; the end-to-end telemetry test asserts that no token, pepper or `Authorization` value is in the exported traces and metrics |
| Authentication, authorisation and rate-limit integration tests pass | **Met** | `cmd/api` integration tests against real PostgreSQL and Redis: key lifecycle, identical 401 for every failure, 429 with headers, Redis outage fallback, `authz.denied` audit |
| `make restore-drill` exists | **Met** | ran in the clean clone: `OK: 4 tables, 5 rows, migration version 2:false restored and verified in 5.907s` |
| Definition of Done satisfied | **Partly met** | see section 2 |

## 2. Definition of Done (`docs/architecture/08-engineering-standards.md` section 3), for E1 as a whole

| Item | Result | Note |
|------|--------|------|
| Requirement and design linked; ADR if architectural | Met | design plan, ADR-026 to ADR-036 |
| Code and unit tests; integration or contract tests | Met | about 7,100 lines of code and 8,600 of tests; integration tests behind the `integration` tag run in CI |
| Error handling classified; failure modes tested | Met | error model; outage, race and failure-path tests |
| Security review item completed | **Partly met** | each slice reviewed its own surface in its ADR; the ASVS requirement-by-requirement pass is the Level 1 gate before E2 exits (ADR-036) |
| Logs, metrics, traces; dashboard and alert for new failure modes | Met | ADR-033; SLO rules, alerts and a Grafana dashboard added in this review. Alerts are visible, not delivered (no Alertmanager) |
| OpenAPI and docs updated; compatibility diff clean | Met | contract, drift and breaking-change checks in CI |
| Migration reviewed (expand/contract, lock impact) | Met | append-only checks, per-migration apply and revert, schema snapshot |
| Performance considered (budget, load test on a hot path) | **Not done** | there is no hot path yet and no load test; the tooling decision (X-4) is due before E5 |
| CI green; code review approved | **Partly met** | CI green. The ruleset requires **0 approvals** (single maintainer), so no independent review took place; this was a conscious setting (ADR-029) |
| Deployed to staging, smoke passed; rollback strategy stated | **N/A for staging** | no deployed environment exists (D1). A rollback strategy is written and was run locally (`runbooks/rollback.md`) |
| Roadmap, backlog and ADR statuses updated | Met | in this review |

## 3. Backlog E1-1 to E1-18

| ID | Item | Status | Delivered by |
|----|------|--------|--------------|
| E1-1 | go.mod, Go pin, Makefile, tool versions | Done | S0 |
| E1-2 | typed configuration | Done | S1 |
| E1-3 | logging with redaction | Done | S1 |
| E1-4 | error model | Done | S1 |
| E1-5 | HTTP server | Done | S2, S4, S4b, S5 |
| E1-6 | API-key authentication | Done | S4 (ADR-027) |
| E1-7 | PostgreSQL platform and migrations | Done | S3 (ADR-031) |
| E1-8 | Redis platform and rate limiter | Done | S4b (ADR-032) |
| E1-9 | OpenTelemetry and local collector | Done | S5 (ADR-033), SLO rules and Grafana in this review |
| E1-10 | Docker images and Compose | Done | S6, S7 (ADR-034) |
| E1-11 | CI with scans | Done, IaC scan N/A | S0 to S8 (ADR-029); there is no IaC to scan (D1) |
| E1-12 | OpenAPI v1 skeleton | Done | S2 |
| E1-13 | Terraform skeleton | **Deferred** | blocked until a cloud vendor is chosen (D1) |
| E1-14 | worker skeleton and queue port | Done | S7 (ADR-035) |
| E1-15 | runbook skeletons | Done | S9; `provider-disable` is a skeleton, not runnable |
| E1-16 | repository hygiene | Done | S0 |
| E1-17 | environment guard (SR-19) | Done for the secret store | S3; the guard for the mock connector arrives with the connector (E3) |
| E1-18 | operator listener and failed-authentication handling | Done | S2, S4, S4b |
| X-2 | ASVS adoption ADR | Done | ADR-036 and `docs/security/asvs-coverage.md` |
| X-3 | SLO dashboards and burn alerts | Done as a baseline | recording rules, burn-rate alerts with unit tests, one Grafana dashboard |

## 4. What verification caught during E1

Worth keeping as evidence that the checks do real work, and as a reminder of where mistakes happened:

- An end-to-end test found that API keys whose secret contains `_` were rejected (the parser split on every underscore).
- The CI race detector found a data race in the HTTP server; a Windows machine without a C compiler could not see it, hence `make test-race-docker`.
- Trivy found a HIGH vulnerability in `google.golang.org/grpc` that `govulncheck` did not report, because the vulnerable code is not called.
- Running the SLO rules against the live stack found that the error ratio had no value when there were no errors at all, the healthy case; the rule tests did not cover it until a test for the value was added.
- Executing the runbooks found that a real restore must keep the table owners (the drill's `--no-owner` would break later migrations), and that a rotated pepper leaves old keys `active`.
- A unit test found a nil dereference when closing a partly built service.
- Validating the merged `main` in a fresh clone found that `make stack` could fail on the first start from an empty volume (the database
  health check passed over a Unix socket while PostgreSQL was still initialising and the migration job connected over TCP), and that a Redis
  integration test failed under load because the limiter's 100 ms Redis timeout is short on a busy machine. Both are fixed
  (ADR-034 amendment; the test sets its own timeout), each with evidence that the fix works: the test went from failing 15 of 15 runs under
  CPU load to passing 15 of 15.
- The metrics test found a rate-limit counter that skipped the refusals that matter.
- Mistakes of mine in the process, caught by guards and tests: counts of staged files, a temporary directory created in the wrong place on Windows, and an unfair first verification of the CI gate logic (no `jq` installed).

## 5. Deferred, open and known limits

| Item | Why | Reopens when |
|------|-----|--------------|
| Infrastructure as code (E1-13) and deployment | no cloud vendor (D1) | a vendor is chosen |
| Managed secret store; production-like environments cannot start | no vendor; the local store refuses them by design (SR-19) | with the vendor decision |
| TLS termination, HSTS and trusted-proxy handling (`X-Forwarded-For` is not trusted) | no edge exists | the first deployment behind a proxy |
| Runtime validation of request bodies against the schema and rejection of unknown JSON fields | no endpoint accepts a body yet | the first write endpoint (E3 or E8) |
| Delivery of alerts (no Alertmanager or channel) | no channel chosen; alerts are visible, not sent | before relying on pages |
| Scheduled backups and WAL archiving; the local recovery point is the last manual dump | not built (ADR-023 amendment) | before E7 ships to production |
| `provider-disable` runbook is a skeleton; no provider, flag store or circuit breaker | E3 and E4 | E3 |
| The worker idles; the PostgreSQL queue adapter and its table grants | E8 | E8 |
| Images are built and scanned but not published or signed | no registry (D1) | with the deployment decision |
| ASVS requirement-by-requirement pass | planned per phase (ADR-036) | **Level 1 gate before E2 exits** |
| No independent code review (0 approvals) | single maintainer | when a second maintainer exists |
| No load test, so no measured capacity | no hot path yet | X-4 decision before E5 |
| Failed authentications are logged, not audited | writing them would let anyone fill the audit table (ADR-027) | a rate-limited, aggregated summary if needed |
| The Grafana dashboard was verified through its API (18 queries returned data, provisioning and read-only checked), not by looking at it | no browser session | first manual look |

**Actions for the owner that no commit can do:** enable the repository's *Dependency graph* and turn CodeQL's *default setup* off (the jobs
fail otherwise), and close the two SonarCloud findings on the Dockerfile's tag and digest as accepted.

## 6. Proposed inputs for the E2 design plan

E2 (domain foundation: value objects, offers, itineraries, the offer state machine, reference data, `GET /v1/airlines|airports`) starts with a
design plan and open questions, as E1 did. Questions to settle in it, as a starting list:
- the source and licence of the airline and airport reference data (E2-5 says the licensing check comes first);
- the representation of money and currency, and the rule for comparing prices (C15: no conversion in Phase 1);
- the fingerprint definitions that separate an itinerary from an offer (C11);
- the ASVS Level 1 requirement-level pass, which gates the end of E2 (ADR-036);
- the first write endpoint's validation, which closes the gap on request bodies above.

## 7. Sign-off

Signed on 2026-10-02 by the owner, after the review in [e1-engineering-review.md](e1-engineering-review.md).

- [x] Owner confirms the exit criteria (section 1) and the deferred list (section 5).
- [x] Owner takes the two repository actions of section 5 (verified on 2026-10-02: `dependency-review` succeeded on the PR #18 head and `codeql` succeeded on `main`).
- [ ] E2 design plan started. Left open on purpose: it has not started. It is the first E2 step.

Carried into E2 from the review: R-1 (cache `/readyz`) before any non-loopback listener, R-2 (move `Principal` out of `httpserver`) before the first application service.
