# ADR-005: REST + OpenAPI 3.1 for the public API

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Machine-to-machine consumers, future B2B productization and AI tool derivation need a stable, documented, toolable contract (Constitution §33-§35).

## Decision
REST/JSON under `/v1`. OpenAPI 3.1 is the contract of record, validated in CI; server stubs/validation generated (tool decided in E1, e.g. oapi-codegen). Consistent error model, cursor pagination, idempotency keys, request IDs. Breaking-change diff gate.

## Alternatives considered
- gRPC: great internally; poorer fit for third-party/AI/tool consumers.
- GraphQL: flexible queries complicate rate limiting, caching and cost control for provider-bound operations.

## Consequences
+ Broad client compatibility, cacheable reads.
- Verbose; chatty clients mitigated by purpose-built endpoints. Internal gRPC remains possible after service extraction.

## Rejected options
Hand-written undocumented endpoints; a spec produced only from code annotations without review.

## Decision note (D5, 2026-09-30)
Router: stdlib `net/http` + **chi**. Codegen: **oapi-codegen**. Load testing: **k6** (ADR for tool recorded in E1).

## Decision note (E1 S2, 2026-10-01): OpenAPI 3.0.3
The contract is written in **OpenAPI 3.0.3**, not 3.1. oapi-codegen v2.8.0 only partially supports 3.1, and the
server, strict handlers and request/response types are generated from the contract. The contract moves to 3.1 when the
tooling supports it without losing generation. The contract of record is `api/openapi/v1/openapi.yaml`;
generated code is `internal/platform/httpserver/openapi/api.gen.go` (never edited by hand, checked for drift in CI).
Breaking changes within `/v1` are detected in CI with oasdiff against `main` (`scripts/openapi-breaking.sh`).
