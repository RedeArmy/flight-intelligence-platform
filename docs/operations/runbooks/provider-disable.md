# Runbook: take a flight provider out of service

## Status
**State:** Skeleton
**Last verified:** never

**This runbook cannot be run today.** The platform has no provider adapter, no feature-flag store, no circuit breaker and no
administration command for either: `internal/provider` and `internal/platform/featureflags` hold no code, and only a mock
provider is allowed until E4 (D2). Nothing below was executed. It records the procedure the design calls for, so that the people
who build E3 and E4 build the means to run it and then verify it.

Do not mark this runbook `Ready` until every item in "Before you start" exists and every step has been executed and its output
written down here.

## When to use
Design intent, from `docs/operations/reliability-and-observability.md` section 5 and `docs/architecture/05-provider-contract.md`:

| Situation | Severity | Action |
|-----------|----------|--------|
| one provider is down, erroring or slow and the rest work | SEV3 | disable that provider so searches stop waiting for it |
| every provider is down | SEV2 | do not disable them all one by one: this is an incident, not a provider problem |
| a provider's data is wrong (prices, availability) | SEV2 | disable it until the cause is known; no wrong data may be presented as verified |
| the contract, legal basis or credentials of a provider are in doubt | decided by the owner | disable it; credentials: also [key-rotation](key-rotation.md) |
| the cost of a provider exceeds its budget | SEV4 | disable or throttle it, per the cost model |

## Impact
Design intent: searches continue without the provider and return partial results flagged as such; verifications that need that
provider fail with a clear, classified error instead of waiting. Nothing is deleted: history and observations already stored stay.

## Before you start
All of these are missing today. Each is a requirement for whoever delivers the matching epic, and this runbook is not `Ready` until they exist:
- [ ] **A provider adapter** registered in the provider lifecycle (`REGISTERED, CONFIGURED, HEALTHY, DEGRADED, DISABLED`) (E3, E4).
- [ ] **A server-side feature flag per provider**, with owner, purpose, creation date and review date, evaluated deterministically and
  audited on every change (ADR-024). Provider enablement is always flag-gated.
- [ ] **A way to change the flag** that an operator can run (an administration command or endpoint, with the same separation of
  roles as `keyctl`, ADR-027) and that writes an audit event.
- [ ] **A circuit breaker and bulkhead per provider** (the request path of `05-provider-contract.md`) and the metrics that show their
  state (`provider_circuit_state`, `provider_health_state`, `provider_requests_total`, listed in `docs/operations/telemetry.md` when they exist).
- [ ] **A decision on who may disable a provider** and how to reach them.

## Steps
Design intent only. Each step is a placeholder to be replaced with the real command and its observed output.

1. Identify the provider and why: its health state and recent error and latency windows. `TBD (E3): command or dashboard`.
2. Disable it through its feature flag, recording the reason. `TBD (E3): command`. The change must be audited.
3. Confirm the breaker is open or the provider is no longer routed to. `TBD (E3): metric or command`.
4. Tell whoever depends on the provider's results (support, clients) according to the severity.
5. Re-enable later in the reverse order: first confirm the cause is fixed with a probe, then enable the flag, then watch the health
   state reach `HEALTHY` before the incident is closed. `TBD (E3)`.

## Verification
Design intent, to be turned into commands:
- the provider is `DISABLED` and receives no calls (its request counter stops rising);
- a search returns results from the other providers and marks the result as partial;
- the audit trail shows who disabled it, when and why.

## If it goes wrong
To be written with the implementation. At minimum: how to re-enable quickly if the wrong provider was disabled, and what to
do when disabling does not stop the traffic (the breaker and the flag disagree).

## Follow-up
Record the reason and the duration in the incident log; review the flag's `review_by` date so a temporary disable does not become
permanent unnoticed (the stale-flag report of ADR-024); and, for the first real use, turn this skeleton into a verified runbook.
