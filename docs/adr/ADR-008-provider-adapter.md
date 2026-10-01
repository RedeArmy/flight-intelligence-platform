# ADR-008: Provider adapter architecture and gateway

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Airlines differ in access, auth, semantics, limits and terms; NDC is a standard family, not one API (Constitution §12-§17, §75). Provider failures must be isolated (P8).

## Decision
Capability-based small interfaces (Describer, Searcher, Verifier, BookabilityChecker); connectors map to canonical types inside their package. The Provider Gateway handles routing, auth, deadline-aware timeouts, retries (transient only), rate limits, bulkheads, circuit breakers, health, error translation, metrics and provider_requests recording. Capabilities are declared from contract documentation, never assumed.

## Alternatives considered
- One large AirlineConnector interface: forces unsupported methods.
- Gateway as a separate network service: extra hop and failure mode without need.
- Scraping adapters as primary strategy: rejected (Constitution §75).

## Consequences
+ A new provider is a new package plus config, fixtures and docs.
- The gateway is critical code: needs strong tests and observability.

## Rejected options
Provider-native types in the domain; per-provider conditionals in application code.
