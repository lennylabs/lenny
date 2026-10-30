-- The §10.7 per-tenant experiment registry.
--
-- experiment_definitions is tenant-scoped: it carries the
-- lenny_tenant_guard trigger and a row-level security policy, exactly
-- like the other §12.3 tenant-scoped tables. The trigger function and
-- the lenny_app role are created by migration 0002.
--
-- The scalar fields the experimentstore.Experiment struct exposes
-- directly (status, base_runtime, the audit timestamps) are typed
-- columns. The nested ExperimentDefinition body — the variant list
-- and the targeting / child-propagation policy — is stored as a
-- single jsonb document in body, mirroring how sessions stores its
-- workspace plan.

CREATE TABLE experiment_definitions (
    tenant_id    TEXT        NOT NULL REFERENCES tenants(id),
    id           TEXT        NOT NULL,
    -- status is the §10.7 lifecycle state (active, paused, concluded).
    -- The lifecycle transition rules are enforced in application code
    -- at the admin-API boundary.
    status       TEXT        NOT NULL DEFAULT '',
    -- base_runtime is the §10.7 control-group runtime.
    base_runtime TEXT        NOT NULL DEFAULT '',
    -- body holds the nested ExperimentDefinition: the variants list
    -- and the targeting mode / sticky / propagation policy. It is
    -- validated in application code at the admin-API boundary.
    body         JSONB       NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON experiment_definitions
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE experiment_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE experiment_definitions FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON experiment_definitions
    USING (tenant_id = current_setting('app.current_tenant', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON experiment_definitions TO lenny_app;
