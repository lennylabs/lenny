-- Reverses 0078_credential_pool_cache_policy.
ALTER TABLE credential_pools
    DROP COLUMN IF EXISTS cache_policy;
