# Runbooks

Step-by-step procedures for operating the platform (E1-15, `docs/operations/reliability-and-observability.md` section 4).
Today everything runs on the local Docker Compose stack (D1, ADR-025), so the procedures are written for that stack and say
so where the deployed environment will differ.

| Runbook | State | Covers | Last verified |
|---------|-------|--------|---------------|
| [rollback](rollback.md) | Ready | a bad release (image), a bad migration, a bad secret or configuration change | 2026-10-02 |
| [key-rotation](key-rotation.md) | Ready | API keys (routine and compromised), the key pepper, database role passwords, the Redis password | 2026-10-02 |
| [db-restore](db-restore.md) | Ready | taking a backup, restoring it with ownership, swapping it in, proving it | 2026-10-02 |
| [provider-disable](provider-disable.md) | Skeleton | taking a flight provider out of service (nothing to disable yet) | never |

Planned, not written: `queue-drain-replay` (arrives with the queue adapter in E8) and `failover` (after the deployment
decision, ADR-025).

## How to use and maintain them
- **Start from the template** ([TEMPLATE.md](TEMPLATE.md)). A test fails when a runbook is missing a heading or a valid status.
- **A runbook is `Ready` only after someone ran it** and wrote down where and what was not covered. The procedures here were run
  on the local stack on the date shown, and anything that could not be run says so in its Status section. Re-run a runbook
  after changing the code or configuration it depends on, and update the date.
- **Commands assume a POSIX shell** (Git Bash, WSL, Linux, macOS) and run from the repository root. On Windows without `make`,
  use `.\scripts\dev.ps1 <target>` for the targets and a POSIX shell for the rest.
- `COMPOSE` in the commands below stands for `docker compose -f deployments/local/docker-compose.yml`.
- **Severity and escalation** are defined in `docs/operations/reliability-and-observability.md` (SEV1 to SEV4). Backups and
  secrets are sensitive: never paste them into an issue, a chat or a pull request.
