-- §5.2 / §12.6 execution-mode rename: the v1 mode set collapses from
-- (session, task, concurrent) to (session, service). This migration
-- re-keys the enforced runtime_definitions mode constraint and adds the
-- gateway-written per-pod recycle counters to agent_pod_state. The stale
-- mode-enum documentation on the unconstrained columns lived only in the
-- '--' source comments of 0033 and 0084 and is re-keyed in place in those
-- files; the golang-migrate runner tracks the version integer and never
-- re-reads an applied migration body, so editing those schema-neutral
-- comments alters no live schema and needs no statement here.
--
-- The concurrency_style column on the pool table (0040) is the §5.2
-- concurrent sub-variant. The proposal retires it "once concurrencyStyle is
-- removed" (Section 5, Section 13), coupling the column drop with the
-- pkg/gateway/poolstore ConcurrencyStyle field, enum, and validator
-- removal so no live code reads a column the migration has dropped. That
-- Go-side removal lands in the same change as this column drop (the
-- poolstore mode-collapse step), so the still-live concurrent-mode path no
-- longer reads concurrency_style off a re-read row.
--
-- The task_policy JSONB columns on runtime_definitions (0022) and
-- sandbox_warm_pools (0085) carried the removed taskPolicy block. The mode
-- collapse renames them to session_policy: the persistence layer now writes
-- the §5.1 / §5.2 sessionPolicy mirror into the renamed column, so no live
-- code reads a column the persistence layer no longer writes.
--
-- The live, enforced constraint is runtime_definitions_execution_mode_check
-- (0001_initial_schema.up.sql), which no later migration altered. The
-- gateway runtime store writes execution_mode into runtime_definitions
-- with the literal mode string (pkg/gateway/runtimestore/pgstore), so
-- without this re-key the database rejects a service-mode runtime
-- definition with a constraint violation while still permitting the
-- removed 'task' and 'concurrent' values.
--
-- spec: §5.2 (execution modes), §12.6 (agent_pod_state schema).

-- Re-key the enforced execution-mode enum on runtime_definitions to the
-- (session, service) set.
ALTER TABLE runtime_definitions
    DROP CONSTRAINT runtime_definitions_execution_mode_check;
ALTER TABLE runtime_definitions
    ADD CONSTRAINT runtime_definitions_execution_mode_check
        CHECK (execution_mode IN ('session', 'service'));

-- Retire the concurrency_style pool column now that the §5.2 ConcurrencyStyle
-- field and its validators are removed (the former concurrent mode collapses
-- to sessionPolicy.maxConcurrentSessions > 1). max_concurrent survives as the
-- service-mode per-pod request bound.
--
-- §10.5 Phase 3 column drop: the drop is fronted by a DO $$ preflight gate
-- that fails closed if any row still carries a non-default concurrency_style
-- (no live concurrent-mode pool may remain after the mode collapse), and is
-- idempotent (DROP COLUMN IF EXISTS) so a re-run is a no-op.
DO $$
DECLARE remaining bigint;
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'sandbox_warm_pools'
          AND column_name = 'concurrency_style'
    ) THEN
        SELECT COUNT(*) INTO remaining
        FROM sandbox_warm_pools
        WHERE concurrency_style <> '';
        IF remaining > 0 THEN
            RAISE EXCEPTION 'Phase 3 gate failed: % pool(s) still carry a non-empty concurrency_style; the §5.2 mode collapse requires concurrency_style to be cleared before the column is dropped', remaining;
        END IF;
    END IF;
END $$;
ALTER TABLE sandbox_warm_pools
    DROP COLUMN IF EXISTS concurrency_style;

-- Rename the task_policy JSONB columns to session_policy on both the
-- runtime registry and the pool registry. The persistence layer now writes
-- the sessionPolicy mirror (maxConcurrentSessions, recycle.*, the cleanup
-- and exhaustion knobs) into these columns.
ALTER TABLE runtime_definitions
    RENAME COLUMN task_policy TO session_policy;
ALTER TABLE sandbox_warm_pools
    RENAME COLUMN task_policy TO session_policy;

-- Add the nullable gateway-written per-pod recycle counters to
-- agent_pod_state. sessions_served is incremented at each session
-- release (ReportSessionScrub) and evaluated against
-- recycle.maxSessionsPerPod; scrub_failure_count is incremented on each
-- failed whole-pod scrub (ReportPodScrub) and evaluated against
-- recycle.maxScrubFailures. Both are NULL until the gateway first
-- writes them, the exception to the WarmPoolController-maintained mirror.
ALTER TABLE agent_pod_state
    ADD COLUMN sessions_served     INTEGER,
    ADD COLUMN scrub_failure_count INTEGER;
