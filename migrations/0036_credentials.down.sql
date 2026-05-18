-- Reverses 0036_credentials. Dropping the table cascades its
-- lenny_tenant_guard trigger, RLS policy, and lenny_app grants.
DROP TABLE IF EXISTS credentials;
