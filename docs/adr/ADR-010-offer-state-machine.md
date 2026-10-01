# ADR-010: Offer state machine with append-only transitions

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Search is not verified, and verified is not bookable (INV-1, INV-2, INV-12). Explicit auditable states are needed.

## Decision
States: OBSERVED, AVAILABLE, VERIFIED, BOOKABLE; failure/terminal: PRICE_CHANGED, SOLD_OUT, EXPIRED, PROVIDER_ERROR (transient), INVALID. Transitions recorded in `offer_status_events` (append-only); current status derived. Bookability is capability-gated. Illegal transitions are rejected in the domain with an exhaustive table test.

## Alternatives considered
- Mutable status column only: loses the audit trail.
- Boolean flags (isVerified etc.): permits impossible combinations.

## Consequences
+ Auditable, testable.
- Slightly more reads (mitigated by a denormalized current status updated in the same transaction).

## Rejected options
Treating any search result as purchasable.
