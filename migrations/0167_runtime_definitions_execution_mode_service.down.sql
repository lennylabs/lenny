-- Reverse 0167: restore the (session, task, concurrent) execution-mode
-- enum, the stale column comments, the concurrency_style column, and
-- drop the agent_pod_state recycle counters.
--
-- spec: §5.2 (execution modes), §12.6 (agent_pod_state schema).

ALTER TABLE agent_pod_state
    DROP COLUMN IF EXISTS sessions_served,
    DROP COLUMN IF EXISTS scrub_failure_count;

-- Restore concurrency_style with its original definition from 0040.
ALTER TABLE sandbox_warm_pools
    ADD COLUMN concurrency_style TEXT NOT NULL DEFAULT '';

-- Restore each column's original comment meaning: execution_mode names
-- the §5.2 mode set; scrub_policy carries the gating clause keyed to the
-- old pod-reuse modes (0084:15-19, the only column whose comment named
-- 'task' or 'concurrent' as a gating condition).
COMMENT ON COLUMN sandbox_warm_pools.execution_mode IS 'the §5.2 mode (session, task, concurrent)';
COMMENT ON COLUMN sessions.execution_mode IS 'the §5.2 mode (session, task, concurrent)';
COMMENT ON COLUMN sessions.scrub_policy IS 'the §7.1 scrub-policy string; set only when execution_mode is ''task'' or ''concurrent''';

ALTER TABLE runtime_definitions
    DROP CONSTRAINT runtime_definitions_execution_mode_check;
ALTER TABLE runtime_definitions
    ADD CONSTRAINT runtime_definitions_execution_mode_check
        CHECK (execution_mode IN ('session', 'task', 'concurrent'));
