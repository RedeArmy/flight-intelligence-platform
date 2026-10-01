# Protecting `main`

Goal (Constitution section 52): no direct pushes, pull requests only, all checks green, no force pushes, no deletion.
The rule is stored as code in [.github/rulesets/protect-main.json](../../.github/rulesets/protect-main.json).

## Two layers

| Layer | Where | Enforced for | Needs |
|-------|-------|--------------|-------|
| **Local** | `.githooks/pre-commit`, `.githooks/pre-push` (enabled by `make hooks` / `scripts\dev.ps1 hooks`) | Your own machine only; bypassable with `--no-verify` | Nothing |
| **GitHub ruleset** | Repository settings | Everyone, including admins (no bypass actors) | **A plan that supports rulesets/branch protection on this repository** |

Plan note (verify on your account): to my knowledge, branch protection and rulesets on **private** repositories
need GitHub Pro, Team or Enterprise; on **public** repositories they are available on Free. If the repository is private
on a Free plan, the GitHub layer cannot be enabled until you upgrade or make the repository public. The local layer still works.

## What the ruleset enforces

- Pull request required to change `main` (0 required approvals because this is a solo project: GitHub does not let an author approve their own PR; raise the count when a second maintainer exists)
- Required status check **`ci-gate`** (the single aggregate job in `.github/workflows/ci.yml`), branch must be up to date before merging
- Review conversations must be resolved; stale approvals dismissed on new pushes
- Linear history (squash or rebase merges), no force pushes, no branch deletion
- No bypass actors: the owner is bound by the rule too

## One-time setup

1. Merge the PR that introduces the workflow first (the check name `ci-gate` must exist before it can be required; the first run on the PR creates it).
2. GitHub: **Settings -> Rules -> Rulesets -> New ruleset -> Import a ruleset**, choose `.github/rulesets/protect-main.json`, review, set Enforcement to **Active**, save.
3. Settings -> General -> Pull Requests: enable **Allow squash merging** (and rebase if desired), disable merge commits, enable **Automatically delete head branches**.
4. Verify: try `git push origin main` (must be rejected), open a PR with a deliberately failing test (merge button must stay blocked).

CLI alternative (needs `gh` authenticated with repo admin rights):

```powershell
gh api repos/RedeArmy/flight-intelligence-platform/rulesets --method POST --input .github/rulesets/protect-main.json
```

## Adding checks later

Add jobs to `ci.yml` and list them in the `needs` of `ci-gate`. The ruleset keeps requiring only `ci-gate`, so it does not change.
To make SonarCloud's own PR status mandatory as well, see [sonarcloud.md](sonarcloud.md).

## Changing the rule

Edit `protect-main.json` through a PR, re-import it, and record the reason in an ADR if the policy changes (ADR-029).
