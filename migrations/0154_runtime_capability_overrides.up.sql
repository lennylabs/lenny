-- §5.1 line 49: "Capabilities are customizable per tenant, with the
-- platform defaults as described above." A row records a single tenant's
-- override of a subset of a platform-global runtime's §5.1 capabilities
-- block: interaction, injection.supported, injection.modes, preConnect,
-- and the runtime-level sdkWarmBlockingPaths list. The gateway resolves a
-- runtime to its platform default (runtimestore.Resolve) and then
-- overlays the (tenant, runtime) override on top
-- (runtimecapoverride.ResolveForTenant) at every §5.1 capability
-- consumer (mid-session injection gate, SDK-warm preConnect decision,
-- mid-session-upload gate, one_shot interaction gate, and the
-- GET /v1/runtimes discovery exposure).
--
-- The override is stored as a JSONB document whose field set mirrors
-- runtimestore.CapabilityOverride. An absent JSON field means "inherit
-- the runtime's declared value"; a present field replaces it for the
-- tenant only. One row per (tenant_id, runtime_name); the admin PUT
-- upserts and DELETE removes it. runtime_name is a plain column rather
-- than a foreign key because a Runtime is a platform-global record in the
-- gateway's own runtime_definitions store, not a tenant-scoped row.
--
-- The table carries the standard §12.3 tenant-scoped posture: the
-- lenny_tenant_guard BEFORE-ROW trigger rejects a write whose
-- transaction has not set app.current_tenant to the row's tenant, RLS
-- filters reads through current_setting('app.current_tenant'), and
-- lenny_app gets the DML grants.
--
-- spec: §5.1 line 49 — F-5.1.20.

CREATE TABLE runtime_capability_overrides (
    tenant_id    TEXT        NOT NULL REFERENCES tenants(id),
    runtime_name TEXT        NOT NULL,
    override     JSONB       NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, runtime_name)
);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON runtime_capability_overrides
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE runtime_capability_overrides ENABLE ROW LEVEL SECURITY;
ALTER TABLE runtime_capability_overrides FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON runtime_capability_overrides
    USING (tenant_id = current_setting('app.current_tenant', false));

GRANT SELECT, INSERT, UPDATE, DELETE ON runtime_capability_overrides TO lenny_app;
