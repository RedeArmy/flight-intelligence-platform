# Architecture Decision Records

Format: Context, Decision, Alternatives, Consequences, Rejected options, Review date.
Status lifecycle: Proposed, then Accepted | Amended | Rejected, later Superseded. Every architectural
change needs an ADR (Constitution P15). Number sequentially; never delete, supersede instead.

| ADR | Title | Status |
|-----|-------|--------|
| [001](ADR-001-modular-monolith.md) | Modular monolith with two deployables | Accepted |
| [002](ADR-002-go.md) | Go as primary backend language | Accepted |
| [003](ADR-003-postgresql.md) | PostgreSQL as system of record | Accepted |
| [004](ADR-004-redis.md) | Redis for ephemeral state only | Accepted |
| [005](ADR-005-rest-openapi.md) | REST + OpenAPI 3.1 for the public API | Accepted |
| [006](ADR-006-hexagonal.md) | Hexagonal (ports and adapters) architecture | Accepted |
| [007](ADR-007-ddd-boundaries.md) | Domain-Driven Design boundaries and ubiquitous language | Accepted |
| [008](ADR-008-provider-adapter.md) | Provider adapter architecture and gateway | Accepted |
| [009](ADR-009-canonical-model.md) | Canonical flight model | Accepted |
| [010](ADR-010-offer-state-machine.md) | Offer state machine with append-only transitions | Accepted |
| [011](ADR-011-immutable-observations.md) | Immutable historical observations with lineage | Accepted |
| [012](ADR-012-async-workers.md) | Asynchronous worker model | Accepted |
| [013](ADR-013-queue-abstraction.md) | Job queue abstraction backed by PostgreSQL initially | Accepted |
| [014](ADR-014-caching.md) | Caching strategy | Accepted |
| [015](ADR-015-api-versioning.md) | API versioning and deprecation | Accepted |
| [016](ADR-016-authentication.md) | Authentication: API keys now, OAuth 2.1/OIDC later | Accepted |
| [017](ADR-017-authorization.md) | Authorization model | Accepted |
| [018](ADR-018-secrets.md) | Secrets management and service identity | Amended |
| [019](ADR-019-observability.md) | Observability with OpenTelemetry | Accepted |
| [020](ADR-020-events-outbox.md) | Event-ready design with transactional outbox | Accepted |
| [021](ADR-021-ai-tool-boundary.md) | AI tool boundary: Tool Gateway and Policy Engine | Accepted |
| [022](ADR-022-data-retention.md) | Data retention and raw data policy | Accepted |
| [023](ADR-023-disaster-recovery.md) | Disaster recovery strategy | Amended |
| [024](ADR-024-feature-flags.md) | Feature flags with lifecycle | Accepted |
| [025](ADR-025-deployment.md) | Deployment strategy: managed containers, no Kubernetes | Amended |
| [026](ADR-026-error-config-logging-conventions.md) | Error model, configuration and logging conventions | Accepted |
| [027](ADR-027-api-key-format-hashing-rotation.md) | API key format, hashing, verification, rotation and audit | Accepted |
| [028](ADR-028-local-dev-environment.md) | Local developer environment and tooling | Accepted |
| [029](ADR-029-ci-pipeline-and-main-protection.md) | CI pipeline, main protection and architecture tests | Accepted |
| [030](ADR-030-go-version-policy.md) | Go version and toolchain policy | Accepted |
| [031](ADR-031-postgresql-access-roles-migrations-secrets.md) | PostgreSQL access: roles, migrations, secrets and the local database | Accepted |
| [032](ADR-032-rate-limiting.md) | Rate limiting: token buckets, Redis with a local fallback, failed-authentication throttling | Accepted |
| [033](ADR-033-telemetry-implementation.md) | Telemetry implementation: OTLP/HTTP, local providers, untrusted trace context, bounded labels | Accepted |

