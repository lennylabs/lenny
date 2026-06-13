-- Reverse 0167: drop the agent_pod_state recycle counters and restore the
-- (session, task, concurrent) execution-mode enum. Each statement reverses
-- exactly one surface the up introduced, so an up->down cycle returns the
-- schema to its pre-0167 state. The stale mode-enum documentation is
-- re-keyed in the 0033 and 0084 source comment files rather than as
-- Postgres object comments, so the down has no object comment to reverse.
-- The concurrency_style column is not touched here because the up does not
-- drop it (its retirement is coupled with the later poolstore ConcurrencyStyle
-- removal, Section 5 and Section 13).
--
-- spec: §5.2 (execution modes), §12.6 (agent_pod_state schema).

ALTER TABLE agent_pod_state
    DROP COLUMN IF EXISTS sessions_served,
    DROP COLUMN IF EXISTS scrub_failure_count;

ALTER TABLE runtime_definitions
    DROP CONSTRAINT runtime_definitions_execution_mode_check;
ALTER TABLE runtime_definitions
    ADD CONSTRAINT runtime_definitions_execution_mode_check
        CHECK (execution_mode IN ('session', 'task', 'concurrent'));
