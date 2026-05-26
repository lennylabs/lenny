-- §5.2 task-mode taskPolicy block (lines 398-413). The admin pool API
-- accepts the policy nested on a task-mode pool definition; the
-- PoolScalingController copies it onto the matching SandboxTemplate
-- CRD spec.taskPolicy. The column is JSONB so the schema can absorb
-- spec additions (new cleanup strategies, retirement signals) without
-- a forward-only column migration on every change.
--
-- A NULL row means the pool has no task-mode policy attached: required
-- for session and concurrent pools, and rejected for task-mode pools
-- by the gateway-side ValidateTaskPolicy. See spec/05_runtime-registry-
-- and-pool-model.md §5.2 lines 398-475.
ALTER TABLE sandbox_warm_pools
    ADD COLUMN IF NOT EXISTS task_policy JSONB;
