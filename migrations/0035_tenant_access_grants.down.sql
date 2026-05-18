-- Reverses 0035_tenant_access_grants. Dropping the tables cascades the
-- indexes and the lenny_app grants.
DROP TABLE IF EXISTS pool_tenant_access;
DROP TABLE IF EXISTS runtime_tenant_access;
