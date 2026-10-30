-- Reverses 0101_tenant_token_quota_reset_period.
ALTER TABLE tenants
    DROP COLUMN IF EXISTS token_quota_per_window,
    DROP COLUMN IF EXISTS quota_reset_period;
