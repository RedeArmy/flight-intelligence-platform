-- Audit log for authentication, key lifecycle and privileged operations (SR-12). Append-only (INV-3, ADR-011):
-- no role used by the application has UPDATE or DELETE on this table. Retention jobs run under a separate
-- privileged role added with the retention work (E7).

CREATE TABLE audit_events (
    id              uuid        PRIMARY KEY,
    occurred_at     timestamptz NOT NULL DEFAULT now(),
    actor_client_id uuid        REFERENCES api_clients (id),
    actor_label     text,
    action          text        NOT NULL,
    resource_type   text,
    resource_id     text,
    outcome         text        NOT NULL,
    request_id      text,
    details         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT audit_events_action_format CHECK (action ~ '^[a-z][a-z0-9_.]{1,63}$'),
    CONSTRAINT audit_events_outcome_valid CHECK (outcome IN ('success', 'failure', 'denied')),
    CONSTRAINT audit_events_details_object CHECK (jsonb_typeof(details) = 'object')
);

CREATE INDEX audit_events_occurred_at_idx ON audit_events (occurred_at DESC);
CREATE INDEX audit_events_actor_idx ON audit_events (actor_client_id, occurred_at DESC);

REVOKE ALL ON audit_events FROM PUBLIC;

-- Insert-only for writers; reading is limited to the roles that need it.
GRANT INSERT ON audit_events TO fip_app;
GRANT INSERT, SELECT ON audit_events TO fip_admin;
GRANT SELECT ON audit_events TO fip_readonly;
