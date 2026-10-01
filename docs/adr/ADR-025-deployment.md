# ADR-025: Deployment strategy: managed containers, no Kubernetes

- **Status:** Amended (2026-09-30)
- **Date:** 2026-09-30
- **Review date:** 2027-03-31 (earlier if a trigger named in Consequences occurs)
- **Deciders:** tech lead and reviewers (pending baseline approval)

## Context
Small team and no measured need for Kubernetes (Constitution §5, P12). The cloud vendor is undecided (D1).

## Decision
A managed container service (Cloud Run / ECS Fargate / Azure Container Apps class, per D1) behind CDN/WAF and a load balancer; managed PG, Redis, object storage and secret manager; Terraform for everything; GitHub Actions deploys staging, smoke, canary, prod with automated rollback. Autoscale the API on latency/CPU and workers on queue depth/age. Revisit Kubernetes only with measured need.

## Alternatives considered
- Kubernetes now: operational overhead.
- VMs with scripts: manual drift, weaker rollout.

## Consequences
+ Low ops burden.
- Platform feature limits vary by vendor; keep images container-portable.

## Rejected options
Undocumented manual production configuration.

## Amendment (D1, 2026-09-30)
No cloud vendor is selected. For now everything runs **locally via Docker Compose**; no Terraform or cloud
deployment is written. The architecture stays vendor-neutral: no cloud SDKs in code; secrets, object storage,
cache and queue are reached only through ports with local adapters. The managed-container decision above
applies once a vendor is chosen, via a follow-up ADR that also unblocks IaC and the deploy stages of CI/CD.
Kubernetes remains rejected until measured need.
