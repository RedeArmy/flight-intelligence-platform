# ASVS 5.0.0 coverage map (target Level 2)

Adopted by [ADR-036](../adr/ADR-036-owasp-asvs-adoption.md). Chapter titles are those of ASVS 5.0.0 (V1 to V17).

**This is a coverage map, not an assessment.** It says which project controls address each chapter and where the evidence is. No
individual ASVS requirement has been verified yet; the requirement-by-requirement pass is the Level 1 gate before E2 exits, and it is
repeated per phase for the code that phase adds. Nothing here is a certification or a regulatory claim.

Status values: **Implemented** (controls exist and are tested for what E1 added), **Partial** (some controls exist, the gap is named),
**Not yet** (the surface exists or will soon, and the control does not), **N/A** (the surface does not exist; the trigger that makes it
applicable is named).

Last reviewed: 2026-10-02, at the close of E1. `SR-nn` are the requirements of `docs/security/security-architecture.md`.

## Chapters in scope

| Chapter | Status | Controls | Evidence | Gap or next step |
|---------|--------|----------|----------|------------------|
| **V1 Encoding and Sanitization** | Partial | parameterised SQL only (SR-04); JSON encoding by the standard library; the redacting logger keeps credentials out of log output (SR-09) | `internal/platform/database`, `internal/platform/observability/logging` tests (leak tests); gosec and CodeQL in CI | no output encoding for other contexts exists because no other output exists; requirement-level pass pending |
| **V2 Validation and Business Logic** | Partial | the OpenAPI contract is validated in CI and matched against the routes and the access policies; generated parameter binding; request body size limit; typed configuration that fails fast (SR-04) | `internal/platform/httpserver` (contract-vs-routes audit, body limit tests), `internal/platform/config` | **no runtime validation of request bodies against the schema and no rejection of unknown JSON fields yet**, because no endpoint accepts a body; both must arrive with the first write endpoint. Domain validation (value objects, state machine) arrives with E2 |
| **V4 API and Web Service** | Implemented for E1 | OpenAPI as the single contract, generated server, uniform error envelope, rate limits per address, client and failed authentication (SR-08, SR-23), breaking-change check in CI | `api/openapi/v1`, `internal/platform/httpserver`, `internal/platform/ratelimit`, ADR-032 | per-route and per-client limit overrides; idempotency (SR-11, SR-20) arrives with the first write endpoint |
| **V6 Authentication** | Implemented for E1 | API keys of 256 bits, HMAC-SHA-256 with a pepper, constant-time comparison, expiry and revocation, one answer for every failure, throttling of failed attempts before the key is checked (SR-02, SR-23) | `internal/platform/security` (apikey), `internal/platform/apiauth`, `cmd/keyctl`, ADR-027, ADR-032 and their tests, including the end-to-end lifecycle test | user identity and OAuth/OIDC later (ADR-016); no multi-factor, as there are no human users |
| **V8 Authorization** | Implemented for E1 | route policy table that denies by default, role-to-permission table, server-side enforcement, audit of denials (SR-03, SR-21) | `internal/platform/httpserver/policy.go` and the contract-vs-policy audit test, `authz.denied` audit rows | resource scoping (alerts scoped to a client) arrives with the first resource; operator routes exist as a listener but no admin API yet |
| **V11 Cryptography** | Implemented for E1 | `crypto/rand` for keys and identifiers, HMAC-SHA-256 with a server-side pepper, constant-time compare, no custom primitives (SR-02) | `internal/platform/security`, `internal/shared/id` | key and pepper rotation is a manual runbook; a key-management system arrives with a managed secret store |
| **V12 Secure Communication** | Partial | TLS to PostgreSQL (`verify-full`, no `require`), TLS to Redis, HTTPS for telemetry required outside local, TLS 1.2 minimum (SR-01) | `internal/platform/database`, `internal/platform/cache`, `internal/platform/config` rules | **the public listener serves plain HTTP and leaves TLS to an edge that does not exist yet**; HSTS is the edge's job; inside the local Compose network traffic is unencrypted by design |
| **V13 Configuration** | Partial | typed validated configuration; secrets only through the secret store, never configuration, images or environment; the local store refuses production-like environments (SR-05, SR-19); hardened containers (SR-17); pinned dependencies, images and CI actions (SR-13) | `internal/platform/config`, `internal/platform/security`, `Dockerfile`, `docker-compose.yml`, `docs/operations/configuration.md` | **no managed secret store, so production-like environments cannot start**; secrets rotation is manual (runbook) |
| **V14 Data Protection** | Partial | secrets and credentials never in logs, errors, responses or telemetry (tests assert it, including the exported OTLP payloads); backups treated as sensitive and kept out of Git and images; least-privilege database roles with append-only audit (SR-09, SR-15, SR-24) | logging leak tests, telemetry end-to-end test, privilege-matrix test, `docs/operations/runbooks/db-restore.md` | no data classification yet; encryption at rest depends on the deployment (SR-16); no personal data is stored yet |
| **V15 Secure Coding and Architecture** | Implemented for E1 | enforced dependency rules between layers, vulnerability and secret scanning, SAST, SBOM per image, image scanning, dependency review, protected `main` with one required check (SR-13) | `internal/archtest`, `ci-gate` jobs, `docs/operations/container-ci.md`, ADR-029 | provenance and image signing arrive with the deployment decision; the outbound HTTP controls (SR-07, SR-10, SR-22) arrive with E3 |
| **V16 Security Logging and Error Handling** | Implemented for E1 | structured logs with redaction and request and trace IDs; one error envelope that never returns internal detail; append-only audit of key lifecycle and authorisation denials (SR-09, SR-12); failed authentications logged with reason and key prefix | `internal/platform/observability`, `internal/platform/httpserver/errors.go`, `audit_events`, ADR-027, ADR-033 | failed authentications are not in the audit table by design (ADR-027); no delivery channel for alerts yet (alerts are visible, not sent) |

## Chapters not applicable yet

| Chapter | Why | Becomes applicable when |
|---------|-----|-------------------------|
| **V3 Web Frontend Security** | the platform serves only a JSON API, no HTML, scripts or cookies | any web frontend or documentation UI is served by the platform |
| **V5 File Handling** | no file upload, download or processing | the first file or object-storage feature (the raw store of E7 is opt-in) |
| **V7 Session Management** | the API is stateless; a key is a bearer credential, not a session | users, browser clients or any server-side session |
| **V9 Self-contained Tokens** | API keys are opaque and looked up; no JWT or similar is issued or accepted | tokens that carry claims (for example OIDC ID or access tokens) |
| **V10 OAuth and OIDC** | no user identity yet (ADR-016 defers it) | user identity or delegated access is introduced; this is a re-planning trigger for ADR-036 |
| **V17 WebRTC** | not used and not planned | never expected; revisit only if real-time media is proposed |

## How this document is kept
- The security review in the Definition of Done updates the row of every chapter the change touches, and adds the requirement-level
  mapping for the code it adds.
- A chapter moves out of "not applicable" in the same pull request that adds the surface, with its row filled in.
- The Level 1 gate before E2 exits and the Level 2 gate before leaving the local machine (ADR-036) are recorded here, with their date and who
  signed them off, when they are passed.
