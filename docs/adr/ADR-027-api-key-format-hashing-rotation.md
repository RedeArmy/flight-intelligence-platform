# ADR-027: API key format, hashing, verification, rotation and audit

- **Status:** Accepted (2026-10-01)
- **Date:** 2026-10-01
- **Review date:** 2027-04-01 (earlier if user identity or OAuth/OIDC is introduced, ADR-016)
- **Deciders:** owner (RedeArmy) with E1 plan approval

## Context
ADR-016 chooses API keys for machine clients and ADR-017 defines roles and a deny-by-default permission table. Slice S4 of E1 implements the authenticator. Requirements: SR-05 (secrets only through the secret store), SR-09 (no secrets in logs), SR-12 (audit of key lifecycle), SR-23 (authentication failures must not reveal why they failed), SR-24 (the runtime role must not be able to mint keys).

## Decision
**Format.** `fip_<prefix>_<secret>`. The prefix is 8 characters from `[A-Za-z0-9]`, public, and is the lookup handle; it is drawn with rejection sampling so no character is favoured. The secret is 32 bytes from `crypto/rand` in unpadded base64url (43 characters, 256 bits). The secret can contain `_`, so parsing splits on the first two separators only. A token of any other shape is rejected before any database access.

**Storage.** The database holds `prefix` (unique), `secret_hmac = HMAC-SHA-256(pepper, secret)` (32 bytes, enforced by a check constraint), the expiry, revocation and last-use times, and the owning client. The key itself is never stored and is printed once, at creation, by `cmd/keyctl`. The pepper is the secret `api_key_pepper` in the secret store (at least 32 bytes), not in the database, so a database leak alone does not allow guessing keys offline.

**Why HMAC and not a slow password hash.** The secret is 256 bits of randomness, not a human-chosen password, so brute force is infeasible and a keyed fast MAC is sufficient. A slow KDF (argon2, bcrypt) on every request would be a CPU denial-of-service lever for unauthenticated callers.

**Verification.** Parse, look up by prefix, compute the HMAC of the presented secret and compare in constant time (`hmac.Equal`), then check, in this order, revocation, expiry (a key is invalid at its exact expiry instant) and that the client is active. An unknown prefix is compared against a decoy hash so the work is the same. Every failure returns the same `401 UNAUTHENTICATED` response with `WWW-Authenticate: Bearer` (SR-23); the real reason (`malformed`, `unknown_or_wrong`, `revoked`, `expired`, `client_inactive`) and the public prefix go to the log only, never the secret. If the key store itself fails, the answer is `503 AUTH_UNAVAILABLE`, not 401: the platform cannot decide, and callers must not treat an outage as bad credentials.

**Revocation is immediate.** Nothing is cached between requests, so a revoked key stops working on its next request. Caching would trade this for speed and is a future decision with its own ADR if load requires it.

**Lifetime and rotation.** `keyctl key issue` defaults to 90 days; `-ttl 0` issues a key that never expires and should be exceptional. Rotation is overlap: issue the new key, move the client over, revoke the old one (procedure: [key-rotation](../operations/runbooks/key-rotation.md)). Rotating the pepper invalidates every key (all hashes change), so it is an incident response, not routine; a dual-pepper scheme is deferred until a need appears.

**Last use.** `last_used_at` is refreshed at most every five minutes per key, best effort: a failure is logged and never fails the request. The runtime role can update that single column and nothing else on `api_keys`.

**Administration.** `cmd/keyctl` (client create, key issue, key list, key revoke) connects as `fip_admin` (ADR-031), so a compromised API process cannot mint or revoke keys. Each change and its `audit_events` row are written in one transaction: a change without its audit row cannot exist. Audit details carry names, prefixes and flags, never the secret or the token.

**Audit scope in S4.** Key lifecycle events are audited. Authentication failures are logged with their reason and prefix but are not written to the audit table: they come from unauthenticated callers, so a database write per failure would let anyone fill the audit log and the disk. They are counted as metrics when observability lands (S5). Auditing authorization denials (valid key, missing permission) and aggregated failure summaries is added with the rate limiter (S4b, see [ADR-032](ADR-032-rate-limiting.md)).

## Alternatives considered
- Argon2id or bcrypt for the stored secret: protects low-entropy secrets, which these are not; costs CPU on every call and enables denial of service.
- Plain SHA-256 without a pepper: a database leak would allow checking guesses and confirming leaked keys offline.
- Storing keys encrypted so they can be shown again: needs key management and keeps a recoverable credential; show-once is simpler and safer.
- JWTs as API keys: cannot be revoked immediately without a lookup anyway, and add signing key management.
- Distinct responses for unknown, wrong, expired and revoked keys: friendlier, and a key-enumeration oracle.

## Consequences
+ A database leak does not yield usable keys or an offline guessing oracle; the pepper lives elsewhere.
+ Unknown and wrong keys cost the same and answer the same; revocation takes effect immediately.
+ Key administration and audit are atomic and separated from the runtime role.
- Every authenticated request costs one indexed database read; a short-lived cache is possible later at the price of delayed revocation.
- The pepper is a single point of rotation pain; losing it invalidates every key.
- Authentication failures are not in the audit table until S4b and S5.
- The 5-minute `last_used_at` granularity means it is a hint for hygiene (finding unused keys), not a precise access log.

## Rejected options
Keys in configuration files or the database in plain text; a pepper stored in the database; returning a reason with a 401; treating a key-store outage as an authentication failure.
