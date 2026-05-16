-- Reverses 0012_tenant_min_isolation_profile.
ALTER TABLE tenants
    DROP COLUMN IF EXISTS min_isolation_profile;
