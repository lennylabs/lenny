-- Reverse 0167: drop the agent_pod_state recycle counters, restore the
-- (session, task, concurrent) execution-mode enum, rename the session_policy
-- columns back to task_policy on both registries, and re-add the
-- concurrency_style pool column with its 0040 NOT NULL DEFAULT '' definition.
-- Each statement reverses exactly one surface the up introduced, so an
-- up->down cycle returns the schema to its pre-0167 state. The stale
-- mode-enum documentation is re-keyed in the 0033 and 0084 source comment
-- files rather than as Postgres object comments, so the down has no object
-- comment to reverse.
--
-- spec: §5.2 (execution modes), §12.6 (agent_pod_state schema).

ALTER TABLE agent_pod_state
    DROP COLUMN IF EXISTS sessions_served,
    DROP COLUMN IF EXISTS scrub_failure_count;

-- Rename session_policy back to task_policy on both registries.
ALTER TABLE runtime_definitions
    RENAME COLUMN session_policy TO task_policy;
ALTER TABLE sandbox_warm_pools
    RENAME COLUMN session_policy TO task_policy;

-- Re-add the concurrency_style pool column at its 0040 definition.
ALTER TABLE sandbox_warm_pools
    ADD COLUMN concurrency_style TEXT NOT NULL DEFAULT '';

ALTER TABLE runtime_definitions
    DROP CONSTRAINT runtime_definitions_execution_mode_check;
ALTER TABLE runtime_definitions
    ADD CONSTRAINT runtime_definitions_execution_mode_check
        CHECK (execution_mode IN ('session', 'task', 'concurrent'));
