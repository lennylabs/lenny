-- Reverse 0167: drop the agent_pod_state recycle counters and restore the
-- (session, task, concurrent) execution-mode enum. The up migration only
-- re-keys the runtime_definitions constraint and adds the recycle
-- counters, so the down reverses exactly those two surfaces. The stale
-- mode-enum documentation is re-keyed in the 0033 and 0084 source comment
-- files rather than as Postgres object comments, so the down has no object
-- comment to reverse. The pool-table mode-variant column is left in place
-- by the up (its retirement is deferred to the later poolstore step), so
-- the down has nothing to restore there.
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
