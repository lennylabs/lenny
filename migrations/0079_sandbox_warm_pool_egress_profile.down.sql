-- Reverses 0079_sandbox_warm_pool_egress_profile.
ALTER TABLE sandbox_warm_pools
    DROP COLUMN IF EXISTS egress_profile;
