-- Reverses 0026_custom_roles. Dropping the table cascades its
-- lenny_tenant_guard trigger, RLS policy, and lenny_app grants.
DROP TABLE IF EXISTS custom_roles;
