# Database migrations

Tool: [golang-migrate](https://github.com/golang-migrate/migrate) used as a library by `cmd/migrate` (ADR-003, ADR-031).
Files are embedded in the binary by `migrations.go`, so what runs is exactly what is committed.

## Rules (Constitution sections 54 and 107)

1. **Expand, deploy, backfill, switch, contract.** An `up` migration only adds: new tables, new nullable columns, new indexes.
   It never drops, renames, retypes, truncates or deletes. Removal happens in a later release, after no running version uses
   the old shape. `migrations_test.go` rejects destructive statements in `up` files.
2. **Mixed versions must work.** Never assume every instance upgrades at the same moment: the previous release has to keep
   working against the new schema, and the new release against the previous one during rollout.
3. **Explicit, fail-closed privileges.** Every table is created with `REVOKE ALL ... FROM PUBLIC` and explicit `GRANT`s per role.
   Append-only tables (observations, audit) get no `UPDATE`, `DELETE` or `TRUNCATE` for application roles (INV-3).
4. **Roles are not created here.** `deployments/postgres/roles.sql` creates `fip_migrator`, `fip_app`, `fip_admin` and
   `fip_readonly` before any migration runs. Migrations run as `fip_migrator`.
5. **Down migrations are for local development only** and say so. Production rollbacks are forward fixes.
6. **Large tables:** review lock impact; use `CREATE INDEX CONCURRENTLY` in its own migration file when a table is big.
7. Numbering is contiguous from `0001`; names are `NNNN_description.up.sql` and `.down.sql`.

## Commands
```bash
make local-secrets   # generate local dev passwords into ./secrets (git-ignored)
make db-up           # start the local PostgreSQL (Docker Compose)
make migrate         # apply all migrations as fip_migrator
```
