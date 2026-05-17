-- Reverses 0024_tenant_policy_columns.
ALTER TABLE tenants
    DROP COLUMN IF EXISTS elicitation_content_integrity,
    DROP COLUMN IF EXISTS billing_erasure_policy,
    DROP COLUMN IF EXISTS no_environment_policy;
