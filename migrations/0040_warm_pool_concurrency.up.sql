-- §5.2 concurrent-mode pool configuration for sandbox_warm_pools.
--
-- A `concurrent`-mode pool (Phase 12c) carries the concurrency style,
-- the per-pod slot bound, and the deployer acknowledgment that
-- concurrent slots share the pod process namespace. These columns are
-- NULL/zero/false for session-mode and task-mode pools; the gateway's
-- ValidateConcurrentConfig rejects a non-concurrent pool that sets
-- them.
ALTER TABLE sandbox_warm_pools
    -- concurrency_style is the §5.2 sub-variant: 'workspace' (per-slot
    -- workspace, shared /workspace/shared/) or 'stateless' (no
    -- workspace). Empty for non-concurrent pools.
    ADD COLUMN concurrency_style TEXT NOT NULL DEFAULT '',
    -- max_concurrent is the §5.2 per-pod slot bound. 0 for
    -- non-concurrent pools; >= 1 is required on a concurrent pool.
    ADD COLUMN max_concurrent INTEGER NOT NULL DEFAULT 0,
    -- acknowledge_process_level_isolation records the §5.2 deployer
    -- acknowledgment required to enable concurrent-workspace mode.
    ADD COLUMN acknowledge_process_level_isolation BOOLEAN NOT NULL DEFAULT false,
    -- cleanup_timeout_seconds bounds per-slot cleanup on a
    -- concurrent-workspace pool (§5.2 cleanupTimeoutSeconds).
    ADD COLUMN cleanup_timeout_seconds INTEGER NOT NULL DEFAULT 0,
    -- allow_cross_tenant_reuse mirrors the task-mode field; §5.2
    -- categorically rejects it on a concurrent-mode pool.
    ADD COLUMN allow_cross_tenant_reuse BOOLEAN NOT NULL DEFAULT false;
