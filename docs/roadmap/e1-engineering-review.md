# E1 engineering review (senior review before sign-off)

Date: 2026-10-02. Scope: everything implemented in E0 and E1 on `main` at `09860e1`.
This is a review by the author of the code, not an independent one (see R-9).
None of the findings blocks E2; each has a trigger that says when it must be fixed.

## 1. Measured state

| Item | Value |
|---|---|
| Go production code (excluding generated) | 4,980 lines in about 111 Go files in total with tests |
| Go test code | 8,050 lines (1.6 test lines per production line) |
| Largest packages (production lines) | httpserver 846, database 650, config 458, observability 391, apiauth 381 |
| Import cycles | none; `cmd/*` fan-out up to 10 packages |
| CI on `main` | 15 of 15 checks green; `dependency-review` is skipped on push by design and succeeded on the PR #18 head |
| Sonar | quality gate OK, 0 issues, 0 hotspots, 92.6% coverage |
| Black-box battery | 101 of 101 checks pass |
| Fresh clone | setup 102 s, dev 17 s, `make ci` 126 s, restore drill OK |

## 2. Findings

Severity is the impact if it reached production. "Trigger" is the latest moment it may wait.

| ID | Sev. | Finding | Evidence | Recommendation | Trigger |
|---|---|---|---|---|---|
| R-1 | High | The public `/readyz` runs the PostgreSQL and Redis checks on every call, with no cache and no limit, and names the dependencies. It amplifies anonymous traffic into backend load. | `Health.Readiness` runs all checks per call. Paced test, 50 persistent connections, equal offered rate of about 1,500 req/s: `/healthz` left an authenticated probe at p50 14.8 ms (baseline 14.4 ms) and the flood reached 1,487 req/s; `/readyz` raised the probe to p50 136 ms (baseline 10 ms) and the flood only reached 678 req/s. Single-host loopback, one run each, so the figures are indicative. | Cache the result for a short TTL (1 to 2 s, singleflight), and return only the status publicly, with detail behind the internal listener. | Before E2 exposes any non-loopback listener |
| R-2 | Medium | `Principal`, `Role` and `Authenticator` live in `httpserver`, so an application service cannot use them without importing the HTTP layer. | `internal/platform/httpserver/policy.go` | Move them to a neutral package before the first application service. | First E2 application service |
| R-3 | Medium | The audit write for a 403 is not bounded by the per-client limit, only by the IP bucket. | `guard.protect` order: IP limit, authfail peek, authenticate, authorize (audits denial), client limit | Apply the client limit before auditing denials, or rate-limit denial audits. | Before multi-client use |
| R-4 | Medium | IPv6 clients get a bucket per address, so a /64 bypasses the per-IP limit, and eviction at 100k keys is arbitrary. | `remoteHost` in `middleware.go` returns the raw host | Aggregate IPv6 by /64 and evict by least-recently-used. | Before leaving loopback |
| R-5 | Low | Compose has no `stop_grace_period`; Docker's default of 10 s is shorter than `HTTP_SHUTDOWN_TIMEOUT` (25 s). | `deployments/local/docker-compose.yml`, `config.go` | Set `stop_grace_period` above the shutdown timeout. | Next infra change |
| R-6 | Low | Probe requests produce spans and metrics noise. | Observed in Jaeger and Prometheus | Exclude health routes from tracing, or sample them down. | Next observability change |
| R-7 | Low | Stale `.gitkeep` files in directories that now have content. | `internal/platform/cache`, `internal/platform/queue`, `cmd/api`, `cmd/worker` | Delete them. | Next housekeeping commit |
| R-8 | Info | Known gaps recorded in `e1-closure.md` section 5: no Alertmanager, no scheduled backups or WAL archiving, no request body validation, no load test, no managed secret store or TLS edge. | `e1-closure.md` | Plan inside E2 and the "before leaving local" gate. | As scheduled there |
| R-9 | Info | No independent review: the pull requests have 0 approvals. | GitHub | An independent review of the security-sensitive packages before E2 exits. | E2 exit |

## 3. Method caveat

The first load runs were invalid: unpaced floods exhausted client ports, and Git Bash rewrote the request path.
The valid comparison is the paced one in R-1 only. It is one run per endpoint on one machine, so it supports "readiness is an amplifier", not a capacity figure.
