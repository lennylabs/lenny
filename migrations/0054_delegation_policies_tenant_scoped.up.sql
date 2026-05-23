-- §4.2 line 172 tenant-scope the delegation-policy registry.
--
-- The original 0027_delegation_policies migration carried no
-- tenant_id column because the earlier interpretation of §8.3
-- treated DelegationPolicies as platform-global records. The §4.2
-- line 172 classification table is authoritative and explicitly
-- classifies delegation policies as Tenant-scoped: "tenant_id column
-- + RLS. Each policy belongs to exactly one tenant. platform-admin
-- can read/write across tenants; tenant-admin sees only own
-- tenant's policies."
--
-- This migration backfills the column on existing rows to the
-- built-in `default` tenant and attaches the standard
-- lenny_tenant_guard trigger and lenny_tenant_isolation RLS policy
-- that govern every other tenant-scoped table per migrations 0002 +
-- 0047 + 0051.
--
-- The primary key changes from (name) to (tenant_id, name) so the
-- §12.3 R-01 schema rule holds (tenant_id leads the primary index)
-- and two tenants may register policies under the same logical
-- name without collision.

ALTER TABLE delegation_policies
    ADD COLUMN tenant_id TEXT REFERENCES tenants(id);

UPDATE delegation_policies SET tenant_id = 'default' WHERE tenant_id IS NULL;

INSERT INTO tenants (id, genesis_nonce)
VALUES ('default', '\x00')
ON CONFLICT (id) DO NOTHING;

ALTER TABLE delegation_policies
    ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE delegation_policies DROP CONSTRAINT delegation_policies_pkey;
ALTER TABLE delegation_policies ADD PRIMARY KEY (tenant_id, name);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON delegation_policies
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE delegation_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE delegation_policies FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON delegation_policies
    USING (
        tenant_id = current_setting('app.current_tenant', false)
        OR current_setting('app.current_tenant', false) = '__all__'
    );
