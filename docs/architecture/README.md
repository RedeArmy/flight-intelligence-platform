# Architecture Baseline v1.0 — Flight Intelligence Platform

**Status:** APPROVED 2026-09-30 (owner sign-off: RedeArmy). ADRs 25/25 decided (22 Accepted, 3 Amended); D1-D8 and C1-C15 resolved. Implementation may start at E1; changes to this baseline require an ADR.
(Constitution §65, §84, §110). Approval = all ADRs in `docs/adr` decided (done: 22 Accepted, 3 Amended)
(or explicitly amended) and every item in [open-questions.md](open-questions.md) answered.

Authority order: `docs/CONSTITUTION.md` > this baseline > ADRs > code. Where this baseline narrows or
resolves an ambiguity in the constitution, the resolution is listed in [open-questions.md](open-questions.md)
section "Contradictions and resolutions" so nothing is changed silently.

## Map of the baseline (Constitution §65 items)

| § | Item | Location |
|---|------|----------|
| 1-12 | Vision, scope, non-goals, personas, use cases, FR, NFR, invariants, assumptions, constraints, metrics, risks | [01-product-and-requirements.md](01-product-and-requirements.md) |
| 13-16, 22-23 | Principles, glossary, bounded contexts, context map, domain model, state machines | [02-domain.md](02-domain.md) |
| 17-21, 28 | C4 context/container/component, deployment, sequence diagrams, event catalog | [03-system-architecture.md](03-system-architecture.md) |
| 24, 27, 31, 32 | ERD, data contracts, data classification, privacy | [04-data.md](04-data.md) |
| 25 | API specification | [../api/api-design.md](../api/api-design.md) |
| 26 | Provider adapter contract | [05-provider-contract.md](05-provider-contract.md) |
| 29, 30 | Threat model, security requirements | [../security/security-architecture.md](../security/security-architecture.md) |
| 33, 36, 40 | SLO/SLA, DR, observability | [../operations/reliability-and-observability.md](../operations/reliability-and-observability.md) |
| 34, 35 | Capacity and cost model | [06-capacity-and-cost.md](06-capacity-and-cost.md) |
| 37 | Testing strategy | [../testing/strategy.md](../testing/strategy.md) |
| 38, 39 | CI/CD and IaC design | [07-delivery-cicd-iac.md](07-delivery-cicd-iac.md) |
| 41 | ADRs | [../adr/](../adr/README.md) |
| 42-44, 47 | Repo structure, coding standards, DoD, deprecation policy | [08-engineering-standards.md](08-engineering-standards.md) |
| 45, 46 | Backlog, roadmap | [../roadmap/roadmap.md](../roadmap/roadmap.md), [../roadmap/backlog.md](../roadmap/backlog.md) |

## Conventions used in this baseline

- **Assumption** = stated belief, not verified. Tagged `A-n`.
- **Hypothesis** = numeric target to validate by measurement. Never treated as a commitment.
- **Not invented**: nothing here asserts a specific airline's capabilities, commercial terms, or any
  regulatory requirement. Those are marked `UNKNOWN` and become gating tasks.
- Diagrams are Mermaid (reproducible, diffable, rendered by GitHub).
