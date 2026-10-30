-- Reverses 0133_tenant_erasure_salt.

ALTER TABLE tenants
    DROP COLUMN IF EXISTS erasure_salt;
