-- Reverses 0037_credential_pools. Dropping the table cascades its
-- lenny_tenant_guard trigger, RLS policy, and lenny_app grants.
DROP TABLE IF EXISTS credential_pools;
