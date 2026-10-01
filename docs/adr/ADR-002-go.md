# ADR-002: Go as primary backend language

- **Status:** Accepted (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Need strong concurrency primitives, small static binaries, predictable performance, good ecosystem for HTTP/OTel/Postgres, and operational simplicity (Constitution §5).

## Decision
Go, using a currently supported stable release pinned in `go.mod` and a tool-versions file; upgraded on the official release lifecycle (not an LTS). Idiomatic, stdlib-first (`net/http`, `log/slog`, `context`), minimal dependencies.

## Alternatives considered
- Java/Kotlin: mature but heavier runtime/ops footprint for this team shape.
- TypeScript/Node: weaker for CPU-bound normalization and bounded-concurrency control.
- Rust: higher cost for velocity; no requirement justifies it.

## Consequences
+ Simple deploys, strong concurrency.
- Domain modeling needs deliberate value types; Go upgrade cadence must be calendared (two releases per year).

## Rejected options
Framework-heavy stacks with reflection-driven DI.
