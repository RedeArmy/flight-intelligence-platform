# ADR-015: API versioning and deprecation

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Consumers (including future B2B and AI tools) need stability (Constitution §34, §106).

## Decision
URI major version `/v1`; additive-only within a major; open enums documented; OpenAPI diff gate in CI; `/v2` for breaking changes. Deprecation: announce, Deprecation/Sunset headers, migration guide, notice period (proposed at least 6 months for external consumers), removal date. Same discipline for events and canonical model via schema_version. DB changes use expand/contract.

## Alternatives considered
- Header/media-type versioning: less discoverable.
- No versioning: breaks consumers.

## Consequences
+ Predictable evolution.
- Maintaining two versions during overlap.

## Rejected options
Silent breaking changes; unversioned error codes.
