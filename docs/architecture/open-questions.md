# Open Questions, Contradictions and Resolutions

## A. Contradictions / gaps found in the Constitution (resolved here; please confirm)

| ID | Issue | Proposed resolution |
|----|-------|---------------------|
| C1 | Two phase numberings: product phases 1–6 (§2) vs engineering phases 0–14 (§84–98) | Roadmap uses **E0–E14** (engineering). Product phases (P1–P6) are shown only as outcome labels. |
| C2 | §20 "initial entities" lacks tables needed for stated invariants (providers, verifications, status events, quarantine, outbox/jobs, api keys, opportunities, targets, alerts) | Added (+) in 04-data; ERD is the proposal. |
| C3 | Roles USER/DEVELOPER/OPERATOR/ADMIN/SERVICE (§37) vs M2M-only API keys (§36) | Roles attach to API clients; `USER` role is dormant semantics until OIDC (future). |
| C4 | Rate limiting "by user" (§44) when no users exist in Phase 1 | Limit by IP, API client/key, provider, route, operation; user dimension added with Identity. |
| C5 | Search budget 8 s with 3 s provider timeout and retries (§15–16) can exceed budget | Deadline propagation: each attempt timeout = min(provider timeout, remaining budget); default max 1 retry; none if remaining < provider p50. Tuned by measurement. |
| C6 | 99.9% availability (§47) on a single region/DB | Requires multi-AZ managed PG with PITR; multi-region deferred (A-5). Revisit if SLO burns. |
| C7 | History (E7) comes after Search (E5) but search must produce observations | E5 defines `ObservationRecorder` port with in-memory/no-op adapter; E7 supplies the durable adapter. No data loss concern because E5 has no production traffic. |
| C8 | Redis "job queues where appropriate" (§31) vs "Redis never authoritative" (§19/§33) | Durable jobs (monitoring, notification, historical) use **PostgreSQL queue + outbox**; Redis only for ephemeral work/cache/locks/limits (ADR-013). |
| C9 | "Bookability verification" (§7.2, §10) vs providers that may not offer a confirmation step | Capability-gated (INV-12): max state VERIFIED without provider bookability capability; alerts needing bookability return `422 CONDITION_UNSATISFIABLE` when no provider can satisfy. |
| C10 | Raw-response storage (§23) vs unknown provider terms | Off by default; per-provider opt-in after legal/contract review (A-4, R-2). |
| C11 | Dedup by "fare characteristics" (§18) could merge distinct fares | Two-level fingerprint: itinerary vs offer (02-domain §7). |
| C12 | Alert condition "Bookable" (§77) unsatisfiable for some providers | See C9. |
| C13 | ASVS "5.x" (§40): exact version/levels not stated | **Resolved 2026-10-02 (ADR-036):** ASVS 5.0.0, Level 2 target, Level 1 gate before E2 exits; chapter-level map in `docs/security/asvs-coverage.md`. |
| C14 | Opportunity vs anomaly flow (§27) while ML is deferred (E10) | Phase 1 anomaly = deterministic outlier rule (e.g. robust z-score/percentile vs history) flagged as *candidate*; only verified candidates become opportunities. |
| C15 | Currency handling unspecified | No conversion in Phase 1; compare only within same currency (INV-11). |

## B. Decisions needed from you (block approval)

| ID | Decision | Options / my recommendation |
|----|----------|------------------------------|
| D1 | Cloud vendor | AWS / GCP / Azure. Recommendation: pick the one you already have credits/skills for; architecture is neutral. Affects ADR-025, Terraform modules, secret manager, queue migration target. |
| D2 | First real provider / data channel | Must be an **authorized** channel you can actually access. I will not assume any airline's capabilities. Until chosen, E4 uses mock + documented onboarding. |
| D3 | Go module path / GitHub org | e.g. `github.com/<org>/flight-intelligence-platform` |
| D4 | Job queue (Phase 1) | Recommended: PostgreSQL (`SKIP LOCKED`) + outbox. Alternative: Redis streams (rejected for durability, C8). |
| D5 | DB migration tool and API/config libs | Recommend small, boring set (e.g. goose or golang-migrate; pgx; chi or stdlib `net/http`; oapi-codegen) — final in ADRs with team input. |
| D6 | **Keep MIT** (existing `LICENSE`, copyright Eder Yafeth Garcia Quiroa); CODEOWNERS `@RedeArmy` for all paths | Supersedes the earlier "proprietary" answer. No LICENSE change. Provider contracts, credentials and any third-party data must still never be committed. |
| D7 | Legal/compliance owner and retention numbers | Raw data retention, observation retention, Guatemala regulatory review before E12/E13. No legal claim is made in these docs. |
| D8 | Team size / on-call reality | Shapes incident process and how much we automate in E1. |

## C. Review gates before "Baseline approved"

1. D1–D4 answered. 2. All 25 ADRs reviewed (Proposed → Accepted/Amended). 3. Contradictions A confirmed. 4. Threat model reviewed by a second person. 5. Roadmap E1 scope agreed.

## D. Decision log (answered 2026-09-30)

| ID | Decision | Consequence |
|----|----------|-------------|
| D1 | **Vendor-neutral architecture; no cloud vendor chosen; no Terraform for now; everything runs locally** | ADR-025 amended: deployment is Docker Compose locally. Terraform/IaC (backlog E1-13) and cloud deploy stages of CI/CD are DEFERRED until a vendor is chosen. Code must not import any cloud SDK; secrets/object storage/queue accessed only through ports with local adapters (env/file secrets, local filesystem or MinIO-class object store). |
| D2 | **Mock connector only for now** | E4 (first real provider) stays blocked until an authorized channel is available. E1-E3 and E5-E9 proceed against the mock connector. Provider/commercial access track continues in parallel. |
| D3 | **Module path `github.com/RedeArmy/flight-intelligence-platform`** | Used in `go.mod` at E1-1. Taken from `origin`. |
| D4 | **PostgreSQL queue + outbox behind `JobQueue`** | ADR-013 accepted as written. |

All of D5-D8 are now answered, see section E.
Gate update: "Baseline approved" now requires D1-D4 (done), ADR review, contradictions confirmed (section A), threat-model second review.

## E. Remaining decisions and confirmations (answered 2026-09-30)

| ID | Decision | Consequence |
|----|----------|-------------|
| D5 | **Boring default tooling**: golang-migrate, pgx, stdlib `net/http` + chi, oapi-codegen, golangci-lint, k6 | Each gets a short ADR note in E1 (ADR-003/005 follow-ups). No frameworks, no ORM. |
| D6 | **Keep MIT** (existing `LICENSE`, copyright Eder Yafeth Garcia Quiroa); CODEOWNERS `@RedeArmy` for all paths | Supersedes the earlier "proprietary" answer. No LICENSE change. Provider contracts, credentials and any third-party data must still never be committed. |
| D7 | **Keep proposed retention defaults**; project owner owns legal review; legal review before E7 (retention) and again before E12/E13 (Guatemala travel-agency rules) | ADR-022 numbers stand as proposals. No legal claim is made anywhere in the docs. |
| D8 | **Solo developer** | Process scaled down: reviewer = owner + automated gates + an AI review pass on risky changes; single alert channel, no formal rotation, escalation = owner. The "second person reviews the threat model" gate is replaced by a self-review checklist plus an AI review pass, with an external reviewer recommended before any commercial launch. |

Contradictions **C1-C15 are all confirmed as written** in section A (no changes requested).

### Updated approval gate
1. D1-D8 answered: **done**. 2. Contradictions C1-C15 confirmed: **done**. 3. ADR review: **done** (22 Accepted, 3 Amended: 018, 023, 025). 4. Threat-model self-review checklist + AI review pass: **done** (see `docs/security/threat-model/review-2026-09-30.md`; 3 High, 5 Medium findings fixed in the docs). Owner sign-off: **done 2026-09-30**. **Baseline APPROVED.**

### Resolved item L1 — license: keep MIT (2026-09-30)
Owner chose to keep the existing MIT license. Because the code is open, never commit provider contracts, credentials or licensed third-party data (airline/airport datasets must have compatible licenses, checked in E2-5).
