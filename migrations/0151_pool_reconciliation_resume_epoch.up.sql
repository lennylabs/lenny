-- §4.6.2 item 3 condition (c) cross-process resume channel. The admin
-- POST /v1/admin/pools/{name}/resume-reconciliation handler bumps this
-- counter (without touching pool_config_generation, since a resume is
-- not a configuration change) and the PoolScalingController — which runs
-- in a separate process and reads this table as its source of truth —
-- observes the advance on its next reconcile tick to clear the pool's
-- in-memory admission-denial backoff. spec: spec/04_system-components.md
-- §4.6.2 item 3 condition (c).

ALTER TABLE sandbox_warm_pools
    ADD COLUMN IF NOT EXISTS reconciliation_resume_epoch BIGINT NOT NULL DEFAULT 0;
