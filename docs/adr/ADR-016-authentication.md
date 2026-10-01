# ADR-016: Authentication: API keys now, OAuth 2.1/OIDC later

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Initial consumers are machine clients; user-facing identity is future (Constitution §36). We must not invent cryptographic protocols.

## Decision
API keys (`fip_<prefix>_<secret>`), at least 128-bit secrets, hashed at rest, constant-time compare, expiry, revocation, per-key quotas, audited key lifecycle. Future: OAuth 2.1 + OIDC via an external IdP in the Identity context. Service-to-service identity per ADR-018.

## Alternatives considered
- Build custom user auth: rejected.
- Network allow-lists only: insufficient.

## Consequences
+ Simple and adequate for B2B/M2M.
- Key leakage risk mitigated by rotation, scopes, anomaly detection.

## Rejected options
Custom authentication protocols; secrets in query strings.
