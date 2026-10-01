# ADR-009: Canonical flight model

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Heterogeneous provider payloads must normalize into one provider-independent model (Constitution §9).

## Decision
Value-object-rich canonical model (Money, AirportCode, AirlineCode, FlightNumber, Route, OfferID, DateRange, PassengerCount, CabinClass, fingerprints). Internal OfferID (UUIDv7) distinct from provider refs. Two-level fingerprints (itinerary, offer). `normalization_version` stored with every observation. Currency kept as received; no conversion in Phase 1.

## Alternatives considered
- Pass-through JSON with light mapping: pushes provider logic into consumers.
- Adopting an industry schema wholesale: ties the domain to external evolution.

## Consequences
+ Provider independence, strong validation.
- Mapping effort per provider; schema evolution needs versioning discipline.

## Rejected options
Primitive types for money/codes; floating-point money.
