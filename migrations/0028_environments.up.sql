-- The §10.6 per-tenant Environment registry.
--
-- environments is tenant-scoped: it carries the lenny_tenant_guard
-- trigger and a row-level security policy, exactly like the other
-- §12.3 tenant-scoped tables. The trigger function and the lenny_app
-- role are created by migration 0002.
--
-- Scalar fields (name, description, audit timestamps) are typed
-- columns. The nested §10.6 Environment body — members, the RBAC
-- roles they hold, the runtime and connector selectors, the mcp
-- capability filters, and the bilateral cross-environment-delegation
-- declarations — is serialized into the body jsonb column.

CREATE TABLE environments (
    tenant_id   TEXT        NOT NULL REFERENCES tenants(id),
    -- name is the §10.6 identifier and the per-tenant key.
    name        TEXT        NOT NULL,
    -- description is the human-facing label.
    description TEXT        NOT NULL DEFAULT '',
    -- body carries the nested §10.6 Environment fields: members,
    -- runtimeSelector, mcpRuntimeFilters, connectorSelector,
    -- defaultDelegationPolicy, and the cross-environment declarations.
    body        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, name)
);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON environments
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE environments ENABLE ROW LEVEL SECURITY;
ALTER TABLE environments FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON environments
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON environments TO lenny_app;
