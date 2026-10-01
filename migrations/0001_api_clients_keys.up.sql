-- API clients and their keys (ADR-016, ADR-017). Expand-only: creates tables, never alters or drops existing ones.
-- Roles referenced here are created by deployments/postgres/roles.sql before migrations run.
--
-- Privilege model (SR-15, SR-24), explicit and fail-closed: a table without a GRANT is unreachable.
--   fip_app       runtime API: read clients and keys, touch only api_keys.last_used_at
--   fip_admin     operator tooling (cmd/keyctl): create and revoke clients and keys
--   fip_readonly  analysts and support: no access to key material

CREATE TABLE api_clients (
    id         uuid        PRIMARY KEY,
    name       text        NOT NULL,
    role       text        NOT NULL,
    active     boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_clients_name_key UNIQUE (name),
    CONSTRAINT api_clients_name_length CHECK (char_length(name) BETWEEN 1 AND 128),
    CONSTRAINT api_clients_role_valid CHECK (role IN ('USER', 'DEVELOPER', 'OPERATOR', 'ADMIN', 'SERVICE'))
);

CREATE TABLE api_keys (
    id           uuid        PRIMARY KEY,
    client_id    uuid        NOT NULL REFERENCES api_clients (id),
    prefix       text        NOT NULL,
    secret_hmac  bytea       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz,
    revoked_at   timestamptz,
    last_used_at timestamptz,
    CONSTRAINT api_keys_prefix_key UNIQUE (prefix),
    CONSTRAINT api_keys_prefix_format CHECK (prefix ~ '^[A-Za-z0-9]{8}$'),
    CONSTRAINT api_keys_hmac_length CHECK (octet_length(secret_hmac) = 32),
    CONSTRAINT api_keys_expiry_after_creation CHECK (expires_at IS NULL OR expires_at > created_at)
);

CREATE INDEX api_keys_client_id_idx ON api_keys (client_id);

-- Nothing is reachable by default.
REVOKE ALL ON api_clients, api_keys FROM PUBLIC;

-- Runtime API.
GRANT SELECT ON api_clients TO fip_app;
GRANT SELECT ON api_keys TO fip_app;
GRANT UPDATE (last_used_at) ON api_keys TO fip_app;

-- Operator tooling.
GRANT SELECT, INSERT, UPDATE ON api_clients TO fip_admin;
GRANT SELECT, INSERT ON api_keys TO fip_admin;
GRANT UPDATE (revoked_at, expires_at) ON api_keys TO fip_admin;

-- Read-only access never includes the key material (secret_hmac).
GRANT SELECT ON api_clients TO fip_readonly;
GRANT SELECT (id, client_id, prefix, created_at, expires_at, revoked_at, last_used_at) ON api_keys TO fip_readonly;
