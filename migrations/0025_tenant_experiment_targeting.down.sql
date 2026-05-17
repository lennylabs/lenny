-- Reverses 0025_tenant_experiment_targeting.
ALTER TABLE tenants
    DROP COLUMN IF EXISTS experiment_targeting;
