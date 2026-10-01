# 05 — Provider Adapter Contract

Design sketch (signatures illustrate the contract; they are not production code). Constitution §12–§17, §74–§75.

## 1. Capability-based interfaces (no giant interface)

```go
// Illustrative only.
type Describer interface {            // mandatory
    ID() ProviderID
    Capabilities() Capabilities       // declared, versioned, tested by contract tests
}
type Searcher interface {
    Search(ctx context.Context, req SearchRequest) (SearchResult, error)
}
type Verifier interface {             // price/inventory confirmation
    Verify(ctx context.Context, ref ProviderOfferRef) (VerificationResult, error)
}
type BookabilityChecker interface {   // only if the provider truly supports it
    CheckBookability(ctx context.Context, ref ProviderOfferRef) (BookabilityResult, error)
}
// Future, NOT declared now: Booker, Canceller, Refunder, SeatMapper, BaggageProvider, Servicer.
```

The gateway discovers capabilities by interface assertion + `Capabilities()`; consumers
(`shopping`, `verification`) depend on *their own* ports (`ProviderSearch`, `ProviderVerify`) that the
gateway adapter satisfies. Connectors return **canonical** types (`domain` value objects) plus raw
payload handle; provider-native structs never escape the connector package (INV-5).

## 2. Capabilities descriptor

`supports_search`, `supports_price_confirm`, `supports_inventory_check`, `supports_bookability_check`,
`supported_cabins`, `supported_routes` (rules/allow-list; unknown = not assumed), `max_pax`,
`currencies`, `rate_limit` (declared), `data_retention_terms` (raw store allowed? days?),
`normalization_version`. Source of truth = provider contract documentation; **never guessed** (A-2).

## 3. Error taxonomy (connector → gateway → application)

| Class | Retry | Breaker counts | Surface to client |
|-------|-------|----------------|-------------------|
| `Transient` (timeout, network, 5xx) | yes, jittered backoff, deadline-aware | yes | provider outcome `timeout/unavailable` |
| `RateLimited` (429) | yes, honoring `Retry-After` within budget | yes (separate counter) | `rate_limited` |
| `Auth` (credentials invalid/expired) | no | yes + alert (operator) | `provider_misconfigured` (internal detail hidden) |
| `InvalidRequest` | no | no | `unsupported_request` |
| `UnsupportedRoute` | no | no | excluded from provider selection |
| `MalformedResponse` | no | counted as quality failure | quarantine + `provider_error` |
| `NotFound/Expired` (verify) | no | no | offer → `EXPIRED`/`SOLD_OUT` per mapping |
| `Canceled` (ctx) | no | no | n/a |

Errors are typed, wrap causes (`errors.Is/As`), and carry a stable `Code`; never include credentials or full payloads.

## 4. Gateway responsibilities (per call)

routing → feature-flag check → circuit-breaker admit → rate-limit acquire → bulkhead semaphore →
credential fetch (SecretStore port, cached short-lived) → timeout (min(provider timeout, remaining
request budget)) → connector call → retry policy → error translation → metrics/traces → provider_requests
record. Per-provider config: timeout, retries (default max 1 inside search budget), concurrency limit,
rate limit, breaker thresholds, feature flag, credentials ref.

## 5. Provider lifecycle

`REGISTERED → CONFIGURED → HEALTHY → DEGRADED → DISABLED` (see 02-domain §6.2). Health combines
active probe (cheap call or synthetic) with passive error/latency windows.

## 6. Fixtures (per provider, deterministic, no live calls)

`success_search`, `no_results`, `multiple_offers`, `invalid_response`, `price_change`, `sold_out`,
`timeout`, `rate_limit`, `auth_failure`, `schema_change`. Contract test harness (`test/contract`) runs
every connector against these and asserts canonical-mapping golden files, error classification, and
capability declaration consistency.

## 7. Onboarding checklist (gate for production enablement)

1. Capability analysis from official docs/contract (no assumptions). 2. Legal/contract: storage, raw
retention, rate limits, cost. 3. Credential model + secret-manager entries. 4. Adapter. 5. Canonical
mapping + `normalization_version`. 6. Fixtures. 7. Contract tests. 8. Integration tests (sandbox if
offered). 9. Metrics/traces/dashboards. 10. Rate limit + timeout config. 11. Error mapping table.
12. Security review (SSRF allow-list, credential handling). 13. Feature flag, enabled in staging,
then controlled prod. A new provider must touch only `internal/provider/connectors/<name>`, config, fixtures, docs.

## 8. Reference connectors

- `mock` connector (E3): deterministic, configurable latency/failure injection; powers local dev, e2e, resilience tests. **Not** a stand-in for real behavior.
- First real connector (E4): selection pending (D2). Becomes the reference implementation.
