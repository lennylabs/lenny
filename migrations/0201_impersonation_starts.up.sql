-- §13.3 impersonation start records.
--
-- The first transaction of "Platform-admin impersonation" rule 1 (c)
-- writes one row here, in the platform-admin's own tenant, for each
-- impersonation started with a caller bearer that carries a jti. The row
-- names that caller bearer, the target tenant, the impersonation bearer,
-- and the bearer's exp. The impersonation step of a recursive-revocation
-- request (rule 7) reads the rows whose impersonator_jti is a token of the
-- revoked subtree, and runs its update in a target tenant only when a row
-- names that tenant, so it needs no __all__ context.
--
-- tenant_id is not a foreign key to tenants, for the reason issued_tokens
-- gives in migration 0001: a platform identity can operate under a tenant
-- id that has no tenants row. The table is tenant-scoped and carries the
-- §12.3 apparatus every tenant-scoped table uses: the lenny_tenant_guard
-- trigger, ENABLE/FORCE ROW LEVEL SECURITY with the lenny_tenant_isolation
-- policy, and the standard lenny_app grants. The expired-row GC deletes a
-- row once exp passes, and tenant deletion Phase 4 deletes the tenant's
-- rows.
--
-- spec: §12.2 (TokenIssuanceStore), §13.3 (Platform-admin impersonation,
-- rules 1 (c) and 7), §12.3.
CREATE TABLE impersonation_starts (
    tenant_id        TEXT        NOT NULL,
    impersonator_jti TEXT        NOT NULL,
    target_tenant_id TEXT        NOT NULL,
    bearer_jti       TEXT        NOT NULL,
    exp              TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, bearer_jti)
);

CREATE INDEX idx_impersonation_starts_impersonator
    ON impersonation_starts (tenant_id, impersonator_jti);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON impersonation_starts
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE impersonation_starts ENABLE ROW LEVEL SECURITY;
ALTER TABLE impersonation_starts FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON impersonation_starts
    USING (tenant_id = current_setting('app.current_tenant', false));

GRANT SELECT, INSERT, UPDATE, DELETE ON impersonation_starts TO lenny_app;
