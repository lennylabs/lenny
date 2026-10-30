-- The §10.2 per-tenant user registry (build-sequence §18.12).
--
-- users is tenant-scoped: it carries the lenny_tenant_guard trigger
-- and a row-level security policy, exactly like the other §12.3
-- tenant-scoped tables. The trigger function and the lenny_app role
-- are created by migration 0002.

CREATE TABLE users (
    tenant_id    TEXT        NOT NULL REFERENCES tenants(id),
    subject      TEXT        NOT NULL,
    email        TEXT        NOT NULL DEFAULT '',
    display_name TEXT        NOT NULL DEFAULT '',
    -- roles holds the §10.2 RBAC role set. Membership is validated in
    -- application code at the admin-API boundary.
    roles        TEXT[]      NOT NULL DEFAULT '{}',
    -- disabled is the §11.4 soft_disable state.
    disabled     BOOLEAN     NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- deleted_at is the §11.4 hard_disable / soft-delete tombstone.
    deleted_at   TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, subject)
);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON users
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON users
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON users TO lenny_app;
