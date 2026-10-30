-- §4.6.2 pool_config_generation column. The admin API bumps the
-- counter on every write so the gateway-side PoolConfigDrift check
-- can compare it to the lenny.dev/config-generation annotation the
-- PoolScalingController stamps onto the SandboxTemplate /
-- SandboxWarmPool pair. spec: §4.6.2.

ALTER TABLE sandbox_warm_pools
    ADD COLUMN IF NOT EXISTS pool_config_generation BIGINT NOT NULL DEFAULT 1;
