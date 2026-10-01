# Testing Strategy

Pyramid, weighted toward fast deterministic tests; no normal test requires a live airline system.

| Layer | Scope | Tooling (proposal) | Gate |
|-------|-------|--------------------|------|
| Unit | domain rules: Money, validators, state machine (all transition pairs), dedup fingerprints, ranker, statistics, opportunity rules | `testing`, table-driven, property tests for fingerprints/money | PR, `-race` |
| Integration | Postgres repos + migrations (apply from zero, mixed-version), Redis adapters, queue claim/visibility, outbox, gateway with mock provider | testcontainers or CI services | PR |
| Contract | each connector vs fixtures → canonical golden files; error classification; capability declaration consistency; API handlers vs OpenAPI | `test/contract` harness, schema validators | PR |
| End-to-end | critical flows: search (full/partial), verify (ok/price change/sold out/expired), monitor→alert, history query | Compose env + mock providers | PR (smoke subset) + nightly (full) |
| Security | authn/z matrix, key expiry/revocation, injection, SSRF egress block, rate limits, secret/log redaction, XML hardening, AI tool permissions (E11) | unit/integration + SAST/DAST | PR + scheduled |
| Performance | search, verify, history/stat queries, worker throughput | k6 or equivalent with mock provider latency | nightly/pre-release; budgets from SLOs |
| Resilience | provider timeout/500/429/malformed/schema change/invalid creds; Redis, queue, DB failure; worker crash mid-job; shutdown during load | fault injection in mock provider + toxiproxy-class | nightly |
| Migration | expand/contract compatibility; old app + new schema, new app + old schema | CI job | PR touching `migrations/` |
| Data quality | quality pipeline rules, quarantine routing, plausibility thresholds | unit + fixtures | PR |

## Provider fixtures (per connector)
success, no results, multiple offers, invalid response, price change, sold out, timeout, rate limit,
auth failure, schema change (Constitution §57). Stored under `test/fixtures/providers/<name>/`; recorded
from official sandbox/docs where permitted, sanitized of credentials and personal data.

## Rules
- Determinism: injected clock/ID generator; no sleeps; time-bound tests use fake clocks.
- Flaky tests are bugs; quarantine requires a ticket and expiry.
- Coverage is a signal, not the goal: mandatory 100% branch coverage of the offer state machine and money/fingerprint logic; others by risk.
- Every bug fix adds a regression test. Every invariant (INV-n) maps to at least one test (traceability table kept in `docs/testing/invariant-coverage.md`, created in E2).
- Test data contains no real credentials/PII.
