-- Revert the §4.2 line 173 tenant scoping of connectors.

DROP POLICY IF EXISTS lenny_tenant_isolation ON connectors;
ALTER TABLE connectors DISABLE ROW LEVEL SECURITY;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON connectors;

ALTER TABLE connectors DROP CONSTRAINT connectors_pkey;
ALTER TABLE connectors ADD PRIMARY KEY (id);

ALTER TABLE connectors DROP COLUMN tenant_id;
