# Migration checks in CI

The `migrations` job (and `make migration-check` locally) validates the SQL migrations in `migrations/`. It is part of
`ci-gate`, so a pull request cannot merge while it fails. The rules behind it are in
[migrations/README.md](../../migrations/README.md) and [ADR-031](../adr/ADR-031-postgresql-access-roles-migrations-secrets.md).

## What is checked

| # | Check | How | What it catches |
|---|-------|-----|-----------------|
| 1 | **Applied migrations are never edited** | `go run ./scripts/migrationcheck origin/main` compares `migrations/*.sql` with the merge base on `main`; only added files are allowed | Editing, renaming or deleting a migration that already reached `main`, which may have run elsewhere and would leave those databases different from a fresh one |
| 2 | **Migration policy** | `go test ./migrations/` | Gaps in numbering, a missing `down`, destructive statements in an `up` file (`DROP`, `TRUNCATE`, `DELETE`, renames, type changes), a table without `REVOKE ... FROM PUBLIC` and `GRANT`, update or delete grants on append-only tables |
| 3 | **Every migration applies and reverts on its own** | Real PostgreSQL: apply one migration at a time, revert it, re-apply it, compare the schema each time | A migration that fails on top of the previous one, a `down` that does not undo its `up`, a non-deterministic migration |
| 4 | **Round trip leaves no residue** | `up`, then `down` through every migration, then `up` again | Objects left behind by a `down`, and a schema that differs after re-applying |
| 5 | **Schema snapshot** | The schema built by the migrations (tables, owners, columns, constraints, indexes, and table and column privileges per role, including `PUBLIC`) equals `internal/platform/database/migrate/testdata/schema.golden.txt` | Any change to the schema or to who can do what, which must now appear in the pull request diff |
| 6 | **Concurrency, ownership, roles** | Three runners migrate at once; objects are owned by `fip_migrator`; the runtime role cannot migrate | Races, schema objects owned by the wrong role, a runtime role with DDL rights |

Checks 3 to 6 run against the same digest-pinned PostgreSQL image as local development and the integration job.

## Run it locally
```bash
make test-db            # throw-away PostgreSQL on 127.0.0.1:55432 (once)
make migration-check    # Windows without make: .\scripts\dev.ps1 migration-check
```
The database DSN is explicit, so a missing test database fails the run instead of skipping it.

## When a check fails

- **Check 1 (edited migration).** Revert the edit and put the change in a **new** migration (expand now, contract in a later release, ADR-015). If the migration was never merged to `main`, rebase so it is new relative to `main`.
- **Check 2 (policy).** The message names the migration and the rule. A genuine need for a destructive change belongs in a later contract migration, after no running version uses the old shape.
- **Checks 3 and 4.** Fix the `up` or `down` file so applying and reverting are exact inverses.
- **Check 5 (snapshot differs).** If the change is intended, refresh and review the snapshot, then commit it with the migration:
  ```bash
  go test -tags integration -run TestSchemaMatchesGolden ./internal/platform/database/migrate -update
  git diff internal/platform/database/migrate/testdata/schema.golden.txt
  ```
  Read the diff: it is the exact schema and privilege change this pull request makes. If it contains something you did not intend (for example a `PUBLIC` privilege row), fix the migration instead of accepting the snapshot.

## Adding a migration
1. Add `NNNN_name.up.sql` and `NNNN_name.down.sql` (next number, both files). Never touch existing ones.
2. Create tables with `REVOKE ALL ... FROM PUBLIC` and explicit grants.
3. Run `make migration-check`, refresh the snapshot if it differs, and commit the migration and the snapshot together.

No test needs editing when a migration is added: version counts are derived from the embedded files.

## Limits
- The checks run on an empty database. They do not prove a migration is fast or lock-friendly on a large table; review that by hand (see the migrations README, rule 6).
- They do not run the previous release's application against the new schema. Mixed-version safety rests on the expand/contract rules and on review, until there is a previous release to test against.
- A data migration (rewriting existing rows) needs its own fixture-based test; none exists yet.
