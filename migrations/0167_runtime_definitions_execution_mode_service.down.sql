-- Reverse 0167: drop the agent_pod_state recycle counters, clear the
-- object comments the up added, restore the concurrency_style column, and
-- restore the (session, task, concurrent) execution-mode enum. Each
-- statement reverses exactly one surface the up introduced, so an up->down
-- cycle returns the schema to its pre-0167 state.
--
-- spec: §5.2 (execution modes), §12.6 (agent_pod_state schema).

ALTER TABLE agent_pod_state
    DROP COLUMN IF EXISTS sessions_served,
    DROP COLUMN IF EXISTS scrub_failure_count;

-- Clear the object comments the up added. The pre-0167 baseline carried
-- no Postgres object comment on these columns: their documentation lived
-- in the immutable '--' source comments of 0033 and 0084, which no
-- migration ever issued as COMMENT ON statements. The up re-keyed the
-- stale mode-enum documentation by adding object comments, so the down
-- removes those object comments (sets them to NULL) rather than inventing
-- prior comment text that never existed as an object comment.
COMMENT ON COLUMN sandbox_warm_pools.execution_mode IS NULL;
COMMENT ON COLUMN sessions.execution_mode IS NULL;
COMMENT ON COLUMN sessions.scrub_policy IS NULL;

-- Restore the concurrency_style column the up dropped, with its 0040
-- definition (unconstrained TEXT NOT NULL DEFAULT ''). The down does not
-- restore the dropped column's data: the up's Phase 3 gate guaranteed no
-- pool carried a non-empty value before the drop.
ALTER TABLE sandbox_warm_pools
    ADD COLUMN IF NOT EXISTS concurrency_style TEXT NOT NULL DEFAULT '';

ALTER TABLE runtime_definitions
    DROP CONSTRAINT runtime_definitions_execution_mode_check;
ALTER TABLE runtime_definitions
    ADD CONSTRAINT runtime_definitions_execution_mode_check
        CHECK (execution_mode IN ('session', 'task', 'concurrent'));
