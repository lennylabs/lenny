-- Reverses 0003_users. Dropping the table cascades its
-- lenny_tenant_guard trigger, RLS policy, and lenny_app grants.
DROP TABLE IF EXISTS users;
