# ADR-003: PostgreSQL as system of record

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Need transactional integrity, rich querying for history/statistics, JSONB for versioned payloads, mature managed offerings, and a durable queue/outbox without extra infrastructure.

## Decision
Managed PostgreSQL (multi-AZ, PITR) is the authoritative store for all domain data. Schema ownership by context. pgx driver; SQL-first access (no heavy ORM); migration tool chosen in E1 (goose or golang-migrate, recorded then). Expand/contract migrations. Observation tables insert-only for the app role. Partition only after measurement. Money = bigint minor units + ISO 4217.

## Alternatives considered
- NoSQL/document store: weak relational integrity and ad-hoc analytics.
- TimescaleDB/ClickHouse for history: possible later as a read model; not justified yet.
- ORM-first: hides query behavior and index needs.

## Consequences
+ One durable store simplifies DR.
- Single point of contention: separate connection pools per workload, monitor, plan a read replica before partitioning. History growth is a tracked risk (R-10).

## Rejected options
Using Redis or object storage as source of truth.

## Decision note (D5, 2026-09-30)
Migration tool: **golang-migrate**. Driver: **pgx**. No ORM.

## Decision note (E1 S3, 2026-10-01)
Roles, migrations, secrets, TLS policy and the local database are decided in ADR-031. The first migrations create
`api_clients`, `api_keys` and `audit_events` (append-only for every application role).
