# Runbook: back up and restore the database

## Status
**State:** Ready
**Last verified:** 2026-10-02

Executed on the local Compose stack on that date: taking a backup (A), restoring it **with ownership** into a new database,
checking owners, privileges, row counts and migrations on the result (B.1 to B.4), and the rename that swaps a database in
(B.5, on scratch databases). `make restore-drill` is the regular proof that a dump can be restored (ADR-023, ADR-034).

Not covered, and not verified:
- The swap (B.5) was **not** run on the live `fip` database, only on scratch databases, so the first real swap is the first
  time it touches live data: take a backup right before it.
- Restoring after the **loss of the whole data volume** (starting from an empty PostgreSQL). Verify it before relying on it.
- Point-in-time recovery: WAL archiving does not exist yet (ADR-023 amendment), so recovery is to the moment of a dump.

**Local recovery targets.** There is no scheduled backup, so the recovery point is the age of the last dump you took by hand
(RPO) and the time is minutes (the drill dumps, restores and compares the current data in about 6 seconds). The 15 minute RPO and
1 hour RTO of ADR-023 are targets for the future deployed environment; they are not met locally.

## When to use
- Data was deleted or corrupted (a bad migration that damaged data, a wrong `DELETE`, a bug) and the database must go back to an
  earlier state.
- The database cannot be used and a backup exists.
- To **rehearse**: take a backup and restore it into a scratch database (C). Do this regularly; a backup never restored is not a backup.

A migration that only has the wrong *shape* is not a restore case: roll forward with a new migration ([rollback](rollback.md)).
Redis needs no restore: it holds only counters and caches (ADR-004).

## Impact
- **A (backup):** none; `pg_dump` reads a consistent snapshot and does not block writers.
- **B (restore and swap):** the API and worker are **stopped** during the swap, so the API is down for the length of steps 2 to 6
  (about a minute for local-sized data). Writes made after the backup are lost.
- **C (scratch restore):** none for the live database.

## Before you start
- Run from the repository root in a POSIX shell with the stack up. `COMPOSE="docker compose -f deployments/local/docker-compose.yml"`.
- Backups contain **key hashes and all data**. They live in `backups/` (git-ignored and excluded from Docker builds); treat them like
  secrets and never attach them to an issue or a chat. Secrets are **not** in a backup: keep `./secrets` safe separately.
- Write down what you are restoring and why, and which backup file: the name carries the UTC time it was taken.
- The roles must exist in the target PostgreSQL (the init scripts create them when the data volume is first created).

## Steps

### A. Take a backup
1. ```bash
   TS=$(date -u +%Y%m%d-%H%M%S); mkdir -p backups
   $COMPOSE exec -T postgres sh -c 'export PGPASSWORD="$(cat /run/secrets/postgres_superuser_password)"; \
     pg_dump -h localhost -U postgres -Fc -f /tmp/backup.dump fip'
   $COMPOSE cp postgres:/tmp/backup.dump backups/fip-$TS.dump
   $COMPOSE exec -T postgres rm -f /tmp/backup.dump
   ```
   The file is in PostgreSQL's custom format (`-Fc`). Observed: about 13 kB for the local development data.

### B. Restore a backup and swap it in
1. **Copy the backup into the container and restore it into a NEW database**, keeping the original owners. Do **not** add
   `--no-owner`: the tables must stay owned by `fip_migrator`, or later migrations fail.
   ```bash
   $COMPOSE cp backups/fip-TS.dump postgres:/tmp/restore.dump
   $COMPOSE exec -T postgres sh -c 'export PGPASSWORD="$(cat /run/secrets/postgres_superuser_password)"; \
     psql -h localhost -U postgres -d postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE fip_restored" && \
     pg_restore -h localhost -U postgres -d fip_restored --exit-on-error /tmp/restore.dump && echo restored'
   ```
   Expected last line: `restored`.
2. **Verify the restored database before it replaces anything.** Owners and privileges, row counts and the migration state:
   ```bash
   $COMPOSE exec -T postgres sh -c 'export PGPASSWORD="$(cat /run/secrets/postgres_superuser_password)"; \
     psql -h localhost -U postgres -d fip_restored -At -F" | " \
       -c "SELECT tablename, tableowner FROM pg_tables WHERE schemaname = '"'"'public'"'"' ORDER BY 1" \
       -c "SELECT '"'"'fip_app can select api_keys'"'"', has_table_privilege('"'"'fip_app'"'"','"'"'api_keys'"'"','"'"'SELECT'"'"')" \
       -c "SELECT '"'"'fip_app can update audit_events'"'"', has_table_privilege('"'"'fip_app'"'"','"'"'audit_events'"'"','"'"'UPDATE'"'"')"'
   APP_ENV=local POSTGRES_SSLMODE=disable SECRETS_DIR=secrets POSTGRES_DB=fip_restored go run ./cmd/migrate version
   APP_ENV=local POSTGRES_SSLMODE=disable SECRETS_DIR=secrets POSTGRES_DB=fip_restored go run ./cmd/migrate up
   ```
   Expected: every table owned by `fip_migrator`; `fip_app can select api_keys | t`; **`fip_app can update audit_events | f`**
   (the audit log stays append-only, INV-3); `version=2 dirty=false` (or the current version); and `migrate up` ends without error and
   without applying anything. Compare row counts with the live database if you can (`SELECT count(*) FROM api_keys`, and the others).
   **Stop here if anything differs.**
3. **Stop the writers:** `$COMPOSE --profile app stop api worker`. (A host-run API or worker must be stopped by hand.)
4. **Take a fresh backup of the live database now** (step A), so the swap itself can be undone to the moment before it.
5. **Swap by renaming.** No session may be connected to either database; terminate the stragglers and rename in one command:
   ```bash
   $COMPOSE exec -T postgres sh -c 'export PGPASSWORD="$(cat /run/secrets/postgres_superuser_password)"; \
     psql -h localhost -U postgres -d postgres -v ON_ERROR_STOP=1 \
       -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname IN ('"'"'fip'"'"','"'"'fip_restored'"'"') AND pid <> pg_backend_pid()" \
       -c "ALTER DATABASE fip RENAME TO fip_before_restore" \
       -c "ALTER DATABASE fip_restored RENAME TO fip"'
   ```
   Expected: two `ALTER DATABASE` lines. The old database is kept as `fip_before_restore`.
6. **Start the writers:** `$COMPOSE --profile app up -d --wait api worker`.

### C. Restore into a scratch database (rehearsal, or to recover a few rows)
Run step B.1 with a database name that is not used (for example `fip_scratch`) and B.2 to inspect it, then **do not swap**. To
recover specific rows, copy them out with `psql` (for example `\copy`), then drop the scratch database:
`... -c "DROP DATABASE fip_scratch"`. `make restore-drill` automates the dump, restore and comparison of the live database.

## Verification
- `curl -s http://127.0.0.1:8080/readyz` shows `"postgres":"ok"` and `"status":"ready"`, and `$COMPOSE --profile app ps` shows `api`
  and `worker` as `healthy`.
- A key that existed at the time of the backup authenticates: `curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer TOKEN" http://127.0.0.1:8080/v1/whoami`
  prints `200`, and `make keyctl ARGS="key list"` shows the keys of that moment. (A key issued after the backup is gone, as are
  clients created after it: that is the data loss the restore accepts.)
- `make restore-drill` ends with `OK: N tables, M rows, migration version 2:false restored and verified`.

## If it goes wrong
- **The restored database does not verify (B.2):** do nothing to `fip`; drop it (`DROP DATABASE fip_restored`), try another
  backup, or stop and escalate. The live database was never touched.
- **After the swap the platform misbehaves:** stop the writers, undo the swap with the same command reversed
  (`ALTER DATABASE fip RENAME TO fip_restored_bad; ALTER DATABASE fip_before_restore RENAME TO fip`), start the writers, and keep
  both databases for analysis. This is why `fip_before_restore` is kept.
- **Writers cannot reconnect after the swap:** check the role passwords still match the secrets
  ([key-rotation](key-rotation.md), D) and that the databases were renamed as expected (`\l`).
- **No usable backup exists:** the data cannot be recovered by this runbook. Escalate as SEV1 (data loss).

## Follow-up
- Record what happened: which backup, its age (the RPO you actually got), how long the restore took (the RTO you actually got),
  and any step that was unclear. Update this runbook with what you learned and set `Last verified` only for what you re-ran.
- When you are confident (not before), drop the old database: `DROP DATABASE fip_before_restore`. Remove old dumps from `backups/`
  according to how long you must keep them.
- Take a new backup of the restored state.
- If a bad migration caused the loss, add the check that would have caught it (`docs/operations/migrations-ci.md`).
- Missing today and worth deciding: a scheduled backup and WAL archiving, so the recovery point stops depending on someone remembering.
