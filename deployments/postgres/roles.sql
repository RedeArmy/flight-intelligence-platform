-- Database roles and database-level privileges (SR-15, SR-24, ADR-031).
--
-- Run as a superuser against the target database, before the migrations. Idempotent and safe to re-run.
-- Plain SQL only (no psql meta-commands), so the same file is used by Docker init, CI and the Go test harness.
-- Passwords are NOT set here; they come from the secret store and are applied by the environment's setup step.
--
--   fip_migrator   owns the schema objects, runs migrations (DDL). Never used at runtime.
--   fip_app        the API at runtime: least privilege, granted per table by the migrations.
--   fip_admin      operator tooling such as cmd/keyctl.
--   fip_readonly   analysts and support; no access to key material.

DO $roles$
DECLARE
    r text;
BEGIN
    -- Serialises concurrent runs inside this database. Advisory locks are per database, so callers that configure
    -- the same cluster from several databases at once (the test harness) must serialise themselves.
    PERFORM pg_advisory_xact_lock(727274);

    FOREACH r IN ARRAY ARRAY['fip_migrator', 'fip_app', 'fip_admin', 'fip_readonly']
    LOOP
        IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = r) THEN
            EXECUTE format('CREATE ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', r);
        END IF;
    END LOOP;

    -- Nobody gets in by default; connect rights are explicit.
    EXECUTE format('REVOKE ALL ON DATABASE %I FROM PUBLIC', current_database());
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO fip_migrator, fip_app, fip_admin, fip_readonly', current_database());
END
$roles$;

REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO fip_app, fip_admin, fip_readonly;
GRANT USAGE, CREATE ON SCHEMA public TO fip_migrator;
