# SonarCloud (SonarQube Cloud) integration

Purpose: automatic code-quality and security review of every pull request and every merge to `main`
(bugs, vulnerabilities, security hotspots, code smells, duplication, coverage on new code) with a quality gate.
It complements, and does not replace, the CI checks and human review: the gate decision of record for merging is `ci-gate`,
which includes the `sonar` job once enabled.

## Prerequisites (done by the owner; nothing here is configured automatically)

1. A SonarCloud account linked to the GitHub account/organization that owns the repository.
2. Plan fit (verify in SonarCloud): to my knowledge the free tier covers public projects; analyzing a **private** repository needs a paid plan.
3. Import the repository as a project in SonarCloud. **Turn off Automatic Analysis** for it (Project -> Administration -> Analysis Method), because analysis runs from CI to include Go coverage.
4. Create a SonarCloud token (My Account -> Security) with permission to analyze that project.

## Repository configuration

| What | Where | Value |
|------|-------|-------|
| `SONAR_TOKEN` | GitHub **secret** (Settings -> Secrets and variables -> Actions) | the token from step 4. Never commit it. |
| `SONAR_ORGANIZATION` | GitHub **variable** | your SonarCloud organization key |
| `SONAR_PROJECT_KEY` | GitHub **variable** | the project key shown in SonarCloud |
| `SONAR_ENABLED` | GitHub **variable** | `true` to switch the `sonar` job on |

Until `SONAR_ENABLED` is `true` the `sonar` job is skipped and `ci-gate` treats a skipped `sonar` as acceptable.
Once enabled, a failed or cancelled `sonar` job blocks the merge. The job waits for the quality gate
(`sonar.qualitygate.wait=true`), so a failing gate fails the PR check.

## What runs

- Triggers: every pull request from this repository and every push to `main`. Fork PRs are skipped because secrets are unavailable to them.
- Steps: full-history checkout, `make coverage` (writes `coverage.out`), then `SonarSource/sonarqube-scan-action` (pinned by commit SHA).
- Settings: [sonar-project.properties](../../sonar-project.properties) (sources, exclusions, coverage path). Organization and project key come from variables so they are not hard-coded.

## Recommended SonarCloud settings

- Quality gate: the default "Sonar way" on new code (no new bugs/vulnerabilities, hotspots reviewed, coverage and duplication thresholds). Tighten later; do not weaken it to get a PR through.
- New code definition: "previous version" or "reference branch = main".
- Do not rely on Sonar for secrets or dependency vulnerabilities: `gitleaks` and `govulncheck` in CI own those.

## Optional: also require SonarCloud's own PR status

SonarCloud can post a separate commit status/PR decoration through its GitHub app. If you install it and want it mandatory,
add its check name (shown on the first PR, typically "SonarCloud Code Analysis") to `required_status_checks` in
`.github/rulesets/protect-main.json` and re-import the ruleset. This is redundant while the `sonar` job is part of `ci-gate`.

## Troubleshooting

- "Project not found" or auth errors: check `SONAR_ORGANIZATION`, `SONAR_PROJECT_KEY` and that the token can analyze the project.
- "You are running CI analysis while Automatic Analysis is enabled": disable Automatic Analysis (prerequisite 3).
- Coverage shows 0%: confirm `coverage.out` exists at the repository root in the job and `sonar.go.coverage.reportPaths` matches.
