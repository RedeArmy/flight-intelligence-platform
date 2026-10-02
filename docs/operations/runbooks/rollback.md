# Runbook: roll back a release, a migration or a configuration change

## Status
**State:** Ready
**Last verified:** 2026-10-02

Executed on the local Compose stack on that date: rolling the API back to a previous image by retagging it (A.1), the refusal
messages of `migrate down` (B), recreating containers after a secret change (C, the same sequence as in
[key-rotation](key-rotation.md)) and validating and restarting the collector and Prometheus (D).

Not executed: rebuilding from an older commit as a rollback (A.2: the build itself is what `make stack` does and was run many
times, but not for this purpose), and the Git side of reverting a merged pull request. There is no deployed environment: the
automated rollback on a failed health gate of `docs/architecture/07-delivery-cicd-iac.md` does not exist yet (D1, ADR-025).

## When to use
| Symptom | Section |
|---------|---------|
| the API or worker misbehaves after a new image or a code change | A |
| a migration failed, damaged data or made the schema wrong | B |
| the stack broke after a secret or environment change | C |
| traces or metrics vanished after a change to the collector or Prometheus configuration | D |

If data was lost or corrupted, go to [db-restore](db-restore.md) first: rolling back code does not bring data back.
If a key or secret may be compromised, use [key-rotation](key-rotation.md): restoring an old value is not a fix for a leak.

## Impact
- **A:** the container being rolled back restarts (seconds); the API reports not ready meanwhile.
- **B:** none for `down` on a local database; a forward migration follows the normal release.
- **C:** the containers that read the secret restart.
- **D:** traces and metrics are paused while the collector or Prometheus restart; the API and worker are not affected (the telemetry export never blocks them, ADR-033).

## Before you start
- Run from the repository root in a POSIX shell. `COMPOSE="docker compose -f deployments/local/docker-compose.yml"`.
- Know the last version that was good. Images are tagged `:local` and each build overwrites the tag, so **a rollback by image only
  works if the previous image was tagged before the new build**. Make this a habit before building a change you may need to undo:
  ```bash
  docker tag fip-api:local fip-api:previous
  docker tag fip-worker:local fip-worker:previous
  ```
- Rules that hold in every case: never edit, rename or delete a migration that was already applied (CI blocks it); never
  run `migrate down` outside a local or test database (it refuses); never force-push `main` (the ruleset blocks it).

## Steps

### A. Roll back the API or worker
**A.1 Return to the previous image (fastest; needs the tag from "Before you start").**
1. Point the running tag at the previous image and recreate the container **without building**:
   ```bash
   docker tag fip-api:previous fip-api:local
   $COMPOSE --profile app up -d --no-build --force-recreate --no-deps --wait api
   ```
   Repeat with `fip-worker` and `worker` if the worker is affected. Expected: `Container fip-local-api-1 Healthy`.

**A.2 Rebuild from the last good commit (when no previous image exists).**
1. Find the last good commit on `main`: `git log --oneline origin/main -15`.
2. Build from a clean checkout of it, not from a working tree with local changes:
   ```bash
   git worktree add ../fip-rollback GOODSHA
   (cd ../fip-rollback && make stack)
   ```
   `make stack` rebuilds the images from that tree and recreates the containers.
3. Remove the extra tree afterwards: `git worktree remove ../fip-rollback`.

**A.3 Take the change out of `main`.** `main` is protected, so the undo is a pull request: create a branch, `git revert BADSHA`
(squash merges give one commit per pull request), push it, and let `ci-gate` pass before merging.

### B. Roll back a migration
Decide by where the migration has been applied:
| Where it is | What to do |
|-------------|-----------|
| only in an unmerged pull request | fix or drop the pull request; nothing was applied anywhere shared |
| applied to your **local or test** database only | `APP_ENV=local POSTGRES_SSLMODE=disable SECRETS_DIR=secrets go run ./cmd/migrate down -yes 1`, then fix the migration (it is yours to edit until merged) |
| merged, or applied to any shared database | **roll forward**: add a new migration that fixes or undoes the change (expand/contract, `migrations/README.md`); never edit the applied one |
| data was damaged | [db-restore](db-restore.md), then roll forward |

Check where you are: `APP_ENV=local POSTGRES_SSLMODE=disable SECRETS_DIR=secrets go run ./cmd/migrate version` prints
`version=N dirty=false`. A `dirty=true` means a migration stopped half-way: stop and restore from a backup or fix by hand with care.
Observed guards: `down` without `-yes` prints `migrate: down destroys data and needs -yes (local development only)`, and with
`APP_ENV=staging` it prints `migrate: down is refused outside local and test environments; roll forward instead`.
Before merging a forward fix, run `make migration-check` (it needs `make test-db`).

### C. Roll back a secret or an environment change
1. Put the previous value back: restore the saved copy of the file in `./secrets`, or revert the change to
   `deployments/local/docker-compose.yml` with Git.
2. Recreate what reads it: `$COMPOSE --profile app up -d --force-recreate --no-deps --wait api worker` (add `redis` for the Redis
   password; see [key-rotation](key-rotation.md) for the role passwords, which also live in the database).

### D. Roll back the collector or Prometheus configuration
1. Restore the previous file with Git: `git checkout PREVIOUSSHA -- deployments/local/otel-collector.yaml` (or `prometheus.yml`).
2. Validate it before applying:
   ```bash
   docker run --rm -v "$(pwd -W)/deployments/local/otel-collector.yaml:/etc/otelcol/config.yaml:ro" \
     otel/opentelemetry-collector-contrib:0.161.0 validate --config /etc/otelcol/config.yaml
   docker run --rm --entrypoint /bin/promtool -v "$(pwd -W)/deployments/local/prometheus.yml:/p.yml:ro" \
     prom/prometheus:v3.15.0 check config /p.yml
   ```
   (`$(pwd -W)` is the Windows path form in Git Bash; use `$(pwd)` on Linux or macOS, and set `MSYS_NO_PATHCONV=1` on Windows.)
3. Restart them: `$COMPOSE restart otel-collector prometheus`.

## Verification
- `$COMPOSE --profile app ps` shows `api` and `worker` as `healthy`.
- `curl -s http://127.0.0.1:8080/readyz` prints `{"checks":{"postgres":"ok","redis":"ok"},"status":"ready"}`.
- After A.1: the running container uses the previous image:
  `docker inspect fip-local-api-1 --format '{{.Image}}'` equals `docker image inspect fip-api:previous --format '{{.Id}}'`.
  Observed: it did, and a label that only the bad image carried was gone.
- A request works: `curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer TOKEN" http://127.0.0.1:8080/v1/whoami` prints `200`.
- After D: `curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:9090/-/ready` prints `200`, `$COMPOSE logs otel-collector`
  has no errors, and a few requests later `http_server_requests_total` shows in Prometheus (`http://127.0.0.1:9090`).

## If it goes wrong
- **A.1 does not start the previous image:** check that `fip-api:previous` exists (`docker images fip-api`); if it does not, use A.2.
- **The previous version also fails:** the cause is probably not the code: check the secrets and the database
  ([key-rotation](key-rotation.md), [db-restore](db-restore.md)), and `$COMPOSE logs api`.
- **A migration left `dirty=true`:** do not run anything else on that database; restore from a backup ([db-restore](db-restore.md)).
- **You need the bad version back** (for example to investigate): the tag you overwrote is gone, so rebuild it from its commit (A.2).

## Follow-up
- Record what was rolled back, from which commit or image, why, and how long it took. Keep the logs of the bad version.
- Add the test or check that would have caught it before release, and fix forward on a branch.
- If no previous image existed (A.2 was needed), make tagging before a build part of how you release.
- When a deployed environment exists, replace A with its automated rollback and re-verify this runbook (ADR-025).
