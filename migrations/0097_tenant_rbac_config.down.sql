-- Reverses 0097_tenant_rbac_config.
ALTER TABLE tenants
    DROP COLUMN IF EXISTS rbac_config;
