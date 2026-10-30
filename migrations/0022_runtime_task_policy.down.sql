-- Reverses 0022_runtime_task_policy.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS task_policy;
