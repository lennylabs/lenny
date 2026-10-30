-- The §10.2 per-tenant custom-role registry.
--
-- custom_roles is tenant-scoped: it carries the lenny_tenant_guard
-- trigger and a row-level security policy, exactly like the other
-- §12.3 tenant-scoped tables. The trigger function and the lenny_app
-- role are created by migration 0002.

CREATE TABLE custom_roles (
    tenant_id   TEXT        NOT NULL REFERENCES tenants(id),
    -- name is the role identifier, unique within the tenant and
    -- distinct from every built-in §10.2 role. The structural format
    -- ^[a-z0-9][a-z0-9_-]{0,127}$ is validated in application code.
    name        TEXT        NOT NULL,
    -- permissions holds the §10.2 RBAC permission subset. Membership
    -- and the "may not exceed tenant-admin" invariant are validated in
    -- application code at the admin-API boundary.
    permissions TEXT[]      NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, name)
);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON custom_roles
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE custom_roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE custom_roles FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON custom_roles
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON custom_roles TO lenny_app;
