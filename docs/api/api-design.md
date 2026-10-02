# API Design — `/v1`

OpenAPI 3.1 in `api/openapi/v1/` is the contract of record (created in E1); this document is the design
that spec must implement. Breaking-change policy: see 08-engineering-standards §5.

## 1. Conventions

- JSON over HTTPS; `camelCase` fields; RFC 3339 UTC timestamps; money as `{ "amountMinor": 58900, "currency": "USD" }`; IDs opaque strings (`off_…`, `rte_…`).
- **Auth:** `Authorization: Bearer <api-key>`; keys `fip_<prefix>_<secret>`, only a hash is stored; prefix enables lookup and safe log correlation. Roles attach to the API client: `USER`, `DEVELOPER`, `OPERATOR`, `ADMIN`, `SERVICE`. (User identity via OAuth 2.1/OIDC is future; see ADR-016/017.)
- **Request ID:** `X-Request-Id` accepted/generated and echoed; traced with W3C `traceparent`.
- **Pagination:** cursor-based (`pageSize` ≤ 200, `cursor`), response `nextCursor`. **Sorting/filtering:** explicit allow-listed params only.
- **Idempotency:** `Idempotency-Key` required on `POST /alerts` and `POST …/verify`; replay returns stored result for 24 h (Redis fast path, PG durable record).
- **Rate limits:** per API key, per operation class (search, verify, read), headers `RateLimit-*`, `429` with `Retry-After`.
- **Timeouts:** search ≤ ~8 s server deadline (client should allow 10 s); verify ≤ ~10 s; reads ≤ 2 s.
- Non-versioned operational endpoints: `GET /healthz` (liveness), `GET /readyz` (readiness), `GET /metrics` only on internal port.

## 2. Price semantics in responses (INV-2, INV-7)

Every offer exposes distinct fields; never a bare `price`:

```json
{
  "offerId": "off_01J...",
  "status": "OBSERVED",
  "observedPrice": { "amountMinor": 58900, "currency": "USD", "observedAt": "2026-09-30T14:02:11Z" },
  "verifiedPrice": null,
  "bookable": "UNKNOWN",
  "validUntil": null,
  "sources": [ { "provider": "prov_x", "providerOfferRefHash": "…" } ]
}
```

`bookable` ∈ `YES|NO|UNKNOWN`. Docs state: *"observedPrice is what a provider returned at observedAt and is
not a purchase guarantee."*

## 3. Endpoints (Phase 1)

| Method & path | Purpose | Auth role | Idempotent | Notes |
|---------------|---------|-----------|-----------|-------|
| `POST /v1/flights/search` | UC-1 | USER+ | n/a (safe-ish) | Body: origin, destination, departureDate/range, returnDate?, passengers, cabin, filters (maxStops, maxPrice), `providers?` (operator only). Response: `offers[]`, `providerOutcomes[]`, `partial`, `searchId`. |
| `GET /v1/flights/{offerId}` | UC-2 | USER+ | yes | Offer + current status + status history link |
| `POST /v1/flights/{offerId}/verify` | UC-3 | USER+ | Idempotency-Key | Returns `Verification` (4 tri-state facts, observed vs verified price, validUntil). Verification quota independent of search. |
| `GET /v1/routes/{routeId}/statistics` | UC-5 | USER+ | yes | Query: window, departure date range, cabin. Includes `sampleSize`, `calculationVersion`, `dataFreshness`. |
| `GET /v1/routes/{routeId}/history` | UC-4 | USER+ | yes | Query: from, to, cabin, kind=price\|availability, cursor. Rows include lineage summary. |
| `GET /v1/opportunities` | UC-6 | USER+ | yes | Filters: route, maxPrice, minScore, verifiedOnly (default true). Items include `reasonCodes[]`, `calculationVersion`. |
| `POST /v1/alerts` | UC-7 | USER+ | Idempotency-Key | Route, date window, max price, condition (`OBSERVED_BELOW`\|`VERIFIED_BELOW`\|`BOOKABLE_BELOW`), destination ref. |
| `GET /v1/alerts` | UC-7 | USER+ | yes | Scoped to caller's client. |
| `GET /v1/airlines`, `GET /v1/airports` | FR-16 | USER+ | yes | Cacheable (ETag). |
| `GET /v1/providers`, `PATCH /v1/providers/{id}` (flag/state) | UC-8 | OPERATOR | yes | Audit logged. Not part of public API docs. |

Route IDs: opaque `rte_…` resolved from `originAirport-destinationAirport`; `GET /v1/routes?origin=&destination=` helper to resolve (added to OpenAPI in E2).

Alert conditions must be satisfiable by provider capability; if a condition requires bookability and no
enabled provider supports it, creation returns `422 CONDITION_UNSATISFIABLE` (resolves C12).

## 4. Error model (§35)

```json
{ "error": { "code": "OFFER_VERIFICATION_FAILED", "message": "Offer could not be verified",
             "requestId": "req_123", "details": {} } }
```

| HTTP | Codes (stable, documented) |
|------|----------------------------|
| 400 | `INVALID_REQUEST`, `INVALID_AIRPORT`, `INVALID_DATE_RANGE` |
| 401 | `UNAUTHENTICATED` |
| 403 | `FORBIDDEN` |
| 404 | `OFFER_NOT_FOUND`, `ROUTE_NOT_FOUND` |
| 409 | `IDEMPOTENCY_KEY_REUSED`, `OFFER_EXPIRED` |
| 422 | `CONDITION_UNSATISFIABLE`, `VERIFICATION_NOT_SUPPORTED` |
| 429 | `RATE_LIMITED` |
| 502/503/504 | `OFFER_VERIFICATION_FAILED` (provider), `SEARCH_UNAVAILABLE` (all providers failed), `UPSTREAM_TIMEOUT` |
| 500 | `INTERNAL` (generic; no stack traces, no secrets) |

## 5. Compatibility rules

Additive changes only within `/v1` (new optional fields/endpoints/enum values clients must tolerate —
documented as "open enums"). Removing/renaming fields, tightening validation, changing semantics → `/v2`.
CI runs an OpenAPI diff against `main`. Deprecation: announce, `Deprecation`/`Sunset` headers, migration
guide, minimum notice period (proposed 6 months for external consumers, recorded in ADR-015).

## 6. Future API areas (reserved)

Orders, payments, tickets, servicing, travel planning, hotels, activities: separate path prefixes
(`/v1/orders` …) added only with their bounded contexts; AI tool schemas derived from this OpenAPI.

## 7. Implemented so far (E1 S2, 2026-10-01)

The contract of record is [api/openapi/v1/openapi.yaml](../../api/openapi/v1/openapi.yaml) (OpenAPI 3.0.3, see ADR-005 note).
The server and its types are generated from it; routes, access policies and the error envelope are tested against it.

| Operation | Path | Access | Notes |
|-----------|------|--------|-------|
| `getHealthz` | `GET /healthz` | public | Liveness. Unversioned, no dependency checks. |
| `getReadyz` | `GET /readyz` | public | `ready` or `degraded` give 200; `not_ready` (critical check failing, or shutting down) gives 503. The result is cached for one second, so a flood of probes cannot become database load. |
| `getWhoami` | `GET /v1/whoami` | permission `whoami:read` | Returns the calling client and role. Closed (401) until the API-key authenticator lands in S4. |

Conventions in force: every response carries `X-Request-Id`; every error uses the `ErrorResponse` envelope with a stable `code`;
a missing, invalid, expired or revoked key always answers the same 401 with `WWW-Authenticate: Bearer`; unknown paths answer a JSON 404 and wrong
methods a JSON 405. Operator routes (`/metrics`, admin) live on a separate listener and are not part of this contract.
