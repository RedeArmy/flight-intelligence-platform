# 07 — Delivery: CI/CD and Infrastructure as Code

## 1. CI pipeline (GitHub Actions)

Pull request (required, blocking):

```
format (gofmt/goimports) → lint (golangci-lint incl. architecture/import rules) → vet/static analysis
→ unit tests (race detector) → integration tests (PG + Redis via services/testcontainers)
→ contract tests (provider fixtures) → OpenAPI lint + breaking-change diff vs main
→ SAST (e.g. CodeQL/gosec) → dependency scan (govulncheck + dependency review) → secret scan
→ container build + image scan → IaC scan (tfsec/Checkov-class) + terraform validate/plan
→ build artifacts + SBOM
```

Main / release:

```
build → sign image + provenance attestation → push → deploy staging (terraform apply + migrate expand step)
→ smoke tests → canary prod → health gates → full rollout → post-deploy smoke → (auto rollback on gate fail)
```

Specific tools are *proposals* decided in ADR-025/ADR-019 follow-ups; categories are mandatory.

## 2. Repo and branch policy

Protected `main`: PR required, ≥1 review (CODEOWNERS for `domain/`, `provider/`, `migrations/`, `terraform/`, `docs/adr/`),
all checks passing, no direct pushes, linear history, signed/traceable releases (tags + provenance). PR
template: ADR needed? security impact? migration safe (expand/contract)? observability added? tests? docs/OpenAPI?

## 3. Environments

| Env | Purpose | Data | Providers |
|-----|---------|------|-----------|
| local | dev via Docker Compose | synthetic | mock |
| dev | integration sandbox | synthetic | mock (+ sandbox if offered) |
| staging | prod-like, release gate | synthetic/limited | provider sandboxes where available |
| prod | live | real | live (flag-gated) |

Separate cloud accounts/projects, separate Terraform state, separate secrets per env.

## 4. Local development

`make setup | dev | test | integration | lint | security | migrate | generate | openapi` (Constitution §72).
Docker Compose: api, worker, postgres, redis, otel-collector (+ simple backend), mock-providers.
`README` quickstart is part of Definition of Done for E1.

## 5. IaC design (Terraform) — DEFERRED (D1: local-only, no vendor chosen)

The design below is the target once a vendor is selected; do not write Terraform before then.

```
terraform/
  modules/        network, postgres, redis, object-storage, secrets, compute-api, compute-worker,
                  observability, waf-cdn, iam      (small, versioned, reusable)
  environments/   dev | staging | prod   (root modules; remote state + locking; per-env variables)
```

Rules: no manual prod config; state in encrypted remote backend with locking; no secrets in
`*.tfvars` committed (secret manager references only); plan output reviewed in PR; policy checks
in CI; drift detection scheduled; tagging for cost allocation. Vendor module set chosen in D1/ADR-025.

## 6. Database migrations

Versioned SQL migrations (tool via ADR-003), run as a separate pipeline step with a privileged role
before app rollout (expand) and after (contract). Each migration: reviewed for lock impact, reversible
or documented forward-only, tested against a production-sized copy when tables are large.
Never assume all instances upgrade simultaneously (mixed-version safe).

## 7. Release and rollback

Immutable images; blue/green or canary by health gate; rollback = redeploy previous image (DB changes
backward-compatible by construction). Feature flags decouple deploy from release.

## 8. Supply chain

Pinned module versions + checksum DB, Dependabot/Renovate with review, SBOM per build, SLSA-style
provenance, image signing, protected runners, least-privilege short-lived CI cloud credentials (OIDC
federation, no static cloud keys in GitHub), reproducible builds where practical.

## 9. Local-first scope (D1, ADR-025)
Until a vendor is chosen, CI runs only the pull-request pipeline (section 1 up to build, SBOM, container scan).
Skip IaC scan/plan, image signing/push to a registry, staging/prod deploy, canary and cloud OIDC. Those stages are
documented targets, enabled by a follow-up ADR. `terraform/` stays an empty placeholder.
