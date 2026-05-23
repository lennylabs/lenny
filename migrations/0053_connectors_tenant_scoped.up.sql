-- §4.2 line 173 tenant-scope the connector registry.
--
-- The original 0004_connectors migration carried no tenant_id column
-- because the chart-author's earlier interpretation of §9.3 treated
-- connectors as platform-global records (the `visibility` field
-- discriminated tenant vs platform exposure). The §4.2 line 173
-- classification table is authoritative and explicitly classifies
-- connectors as Tenant-scoped: "tenant_id column + RLS. Connector
-- definitions (endpoint URL, auth config) are per-tenant."
--
-- This migration backfills the column on existing rows to the
-- built-in `default` tenant (the only tenant in pre-deployment
-- databases) and attaches the standard lenny_tenant_guard trigger
-- and lenny_tenant_isolation RLS policy that govern every other
-- tenant-scoped table per migrations 0002 + 0047 + 0051.
--
-- The primary key changes from (id) to (tenant_id, id) so the
-- §12.3 R-01 schema rule holds (tenant_id leads the primary index).
-- An id-only secondary index would let queries lookup by raw id,
-- but the spec requires queries to scope by tenant — application
-- code therefore always supplies tenant_id explicitly.

ALTER TABLE connectors
    ADD COLUMN tenant_id TEXT REFERENCES tenants(id);

-- Backfill any pre-existing rows to the built-in default tenant.
-- The constraint below makes the column NOT NULL after the backfill.
UPDATE connectors SET tenant_id = 'default' WHERE tenant_id IS NULL;

-- Ensure the default tenant row exists for new installs that exercise
-- the migration before bootstrap seeds it. The genesis_nonce column
-- is required; a zero byte is a benign placeholder consistent with
-- the dev-mode bootstrap path.
INSERT INTO tenants (id, genesis_nonce)
VALUES ('default', '\x00')
ON CONFLICT (id) DO NOTHING;

ALTER TABLE connectors
    ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE connectors DROP CONSTRAINT connectors_pkey;
ALTER TABLE connectors ADD PRIMARY KEY (tenant_id, id);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON connectors
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE connectors ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectors FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON connectors
    USING (
        tenant_id = current_setting('app.current_tenant', false)
        OR current_setting('app.current_tenant', false) = '__all__'
    );
