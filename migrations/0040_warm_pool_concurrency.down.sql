-- Reverses 0040_warm_pool_concurrency.
ALTER TABLE sandbox_warm_pools
    DROP COLUMN IF EXISTS concurrency_style,
    DROP COLUMN IF EXISTS max_concurrent,
    DROP COLUMN IF EXISTS acknowledge_process_level_isolation,
    DROP COLUMN IF EXISTS cleanup_timeout_seconds,
    DROP COLUMN IF EXISTS allow_cross_tenant_reuse;
