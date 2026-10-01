# ADR-032: Rate limiting: token buckets, Redis with a local fallback, and failed-authentication throttling

- **Status:** Accepted (2026-10-01)
- **Date:** 2026-10-01
- **Review date:** 2027-04-01 (earlier when a reverse proxy is introduced or per-route limits are needed)
- **Deciders:** owner (RedeArmy) with E1 plan approval

## Context
SR-08 requires limits on request volume, SR-23 requires that authentication failures do not become a guessing oracle, and ADR-004 says Redis holds ephemeral state only and that losing it must degrade limits, never correctness. ADR-027 deferred failed-authentication throttling and the audit of authorisation denials to this slice.

## Decision
**Algorithm.** Token bucket. A rule is `Limit` tokens refilled evenly over `Window`; `Limit` is both the burst size and the average rate. Buckets are named by a key and stored per key.

**Three limits on protected routes**, checked in this order (health probes are never limited):
1. `ip:<address>`: every request from one address, before any authentication work, so floods are cheap to refuse. Default 300 per minute.
2. `authfail:<address>`: failed authentications from one address. It is **checked before the key is verified** (a peek that takes nothing) and **charged only when authentication fails with a 401**. An address that has used up its failures gets `429` and never reaches the key check, so guessing is throttled rather than merely logged. Default 10 per minute. A store outage (503) is not charged: an outage must not lock clients out. Valid requests are not charged.
3. `client:<client id>:<operation class>`: requests of one authenticated client, bucketed by operation class (`read` now; `search` and `verify` when those routes exist). Default 600 per minute.

**Responses.** `429 RATE_LIMITED` in the standard error envelope with `Retry-After` and `RateLimit-Limit`, `RateLimit-Remaining` and `RateLimit-Reset` (all in whole seconds or tokens), documented in the OpenAPI contract. `Retry-After` is at least one second.

**Adapters** (`platform/ratelimit`, consumed through a port defined in `httpserver`):
- Redis: one Lua script per call, atomic, using the Redis server clock so instances with different clocks share one bucket. Keys are namespaced `fip:rl:` and expire with their window. A test proves that 50 concurrent callers can take exactly the bucket's size and no more.
- Memory: per process, bounded (100 000 buckets; buckets that have refilled are swept and an arbitrary one is evicted at the cap), so a flood of distinct keys cannot exhaust memory.
- Fallback: tries Redis; on any error uses the memory limiter with **half** of each limit (a single process cannot know how many instances share the load, so it is deliberately conservative) and does not try Redis again for five seconds, so a dead Redis costs one short attempt, not one per request. The transition and the recovery are logged once each.

**Redis is optional.** Without `REDIS_ADDR` the API runs with the memory limiter only and logs a warning that limits are per instance. With it, Redis appears in `/readyz` as a non-critical check: a failure reports `degraded`, never `not_ready` (ADR-004). Redis connections use short timeouts and no retries, TLS is on by default and required in staging and production, and the optional password is the secret `redis_password`.

**Limiter failure policy.** If the limiter itself returns an error (the Fallback never does, so this means a bug) the request is let through and the error is logged. Refusing all traffic because the limiter is broken would turn a defect into an outage.

**Client address.** The address is the TCP peer address. `X-Forwarded-For` is deliberately not trusted: any client could forge it and pick its own bucket. Behind a reverse proxy every client would share the proxy's address; trusted-proxy handling is added with the first proxy deployment and needs its own decision.

**Audit.** An authenticated caller refused for lack of permission (403) is recorded in `audit_events` as `authz.denied` with the client, the route and the request id, written by the runtime role (insert only). The write is bounded to two seconds, survives a cancelled request, and a failure is logged and never changes the response. Failed authentications are still not written to the audit table (ADR-027): they come from unauthenticated callers and would let anyone fill the log.

## Alternatives considered
- Fixed-window counters: simpler, but allow double the limit around a window boundary.
- Sliding-window log: exact, but stores one entry per request.
- Failing closed when Redis is down: safe for limits, an outage for everyone; ADR-004 forbids it.
- Failing open when Redis is down: leaves the API unprotected during exactly the incidents when it matters; the local fallback avoids both.
- Trusting `X-Forwarded-For` by default: spoofable.
- Charging every request to the failure bucket: would throttle legitimate traffic from shared addresses.

## Consequences
+ Floods are refused before authentication; guessing keys is throttled before each verification; one noisy client cannot starve others.
+ Redis loss reduces precision (per-instance, stricter) without taking the API down.
- Behind a proxy all clients share a bucket until trusted-proxy support exists; the limits are per address, so large shared networks (NAT) can be limited together.
- During a Redis outage the effective fleet-wide limit is the per-instance limit times the number of instances, scaled by one half; it is a safety net, not an exact limit.
- Per-minute limits are global defaults; per-route and per-client overrides are future work.

## Rejected options
Trusting client-supplied address headers; unbounded in-memory maps; storing anything but counters in Redis; returning different errors for different authentication failures to explain a throttle.
