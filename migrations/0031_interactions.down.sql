-- Reverses 0031_interactions. Dropping the table cascades its
-- lenny_tenant_guard trigger, RLS policy, lenny_app grants, and the
-- idx_interactions_tenant_user index.
DROP TABLE IF EXISTS interactions;
