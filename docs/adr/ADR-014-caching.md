# ADR-014: Caching strategy

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Reduce provider calls, cost and DB load without serving misleading prices (INV-2).

## Decision
Cache reference data (long TTL, ETag), computed statistics (TTL aligned to the freshness SLO), and short-lived search results keyed by normalized request (TTL of minutes; responses include observedAt). Never present verified/bookable state beyond `validUntil`. Stampede protection (single-flight). Cache failure fails open.

## Alternatives considered
- No caching: higher provider cost and latency.
- Aggressive offer caching: misleading prices.

## Consequences
+ Lower cost.
- Staleness must be explicit in responses (observedAt, dataFreshness).

## Rejected options
Caching verification results past validity; caching without TTL.
