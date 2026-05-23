-- Revert the §4.2 line 172 tenant scoping of delegation_policies.

DROP POLICY IF EXISTS lenny_tenant_isolation ON delegation_policies;
ALTER TABLE delegation_policies DISABLE ROW LEVEL SECURITY;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON delegation_policies;

ALTER TABLE delegation_policies DROP CONSTRAINT delegation_policies_pkey;
ALTER TABLE delegation_policies ADD PRIMARY KEY (name);

ALTER TABLE delegation_policies DROP COLUMN tenant_id;
