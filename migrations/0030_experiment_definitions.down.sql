-- Reverses 0030_experiment_definitions. Dropping the table cascades
-- its lenny_tenant_guard trigger, RLS policy, and lenny_app grants.
DROP TABLE IF EXISTS experiment_definitions;
