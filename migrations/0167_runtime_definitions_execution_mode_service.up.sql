-- §5.2 / §12.6 execution-mode rename: the v1 mode set collapses from
-- (session, task, concurrent) to (session, service). This migration
-- re-keys the enforced runtime_definitions mode constraint, retires the
-- concurrent sub-variant column on the pool table, and adds the
-- gateway-written per-pod recycle counters to agent_pod_state. The stale
-- mode-enum documentation on the unconstrained columns lived only in the
-- '--' source comments of 0033 and 0084 and is re-keyed in place in those
-- files; the golang-migrate runner tracks the version integer and never
-- re-reads an applied migration body, so editing those schema-neutral
-- comments alters no live schema and needs no statement here.
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

-- Retire the concurrency_style column on the pool table. The §5.2 mode
-- collapse removes the concurrent sub-variant the column encoded
-- ('workspace' or 'stateless'), so the column has no successor and is
-- dropped. The gateway pool store stops persisting and scanning the
-- column in the same change (pkg/gateway/poolstore/pgstore), so a pool
-- round-trip against the migrated schema does not error on a missing
-- column. max_concurrent survives as the per-pod bound consumed by
-- service mode and the session-mode concurrency path
-- (sessionPolicy.maxConcurrentSessions).
--
-- This is a §10.5 Phase 3 column drop: the preflight DO $$ gate aborts
-- when any pool still carries a non-empty concurrency_style, so the
-- concurrent sub-variant of a live pool is never silently lost, and the
-- drop is idempotent (DROP COLUMN IF EXISTS, §10.5 line 430). The platform
-- is pre-deployment, so no pool carries the value in practice and the gate
-- passes trivially; it is the §10.5 line 417 contract that the column has
-- no live dependents before it is removed.
-- gate-index: none required; the gate scans the column directly.
DO $$
DECLARE remaining bigint;
BEGIN
    SELECT COUNT(*) INTO remaining
      FROM sandbox_warm_pools
     WHERE concurrency_style <> '';
    IF remaining > 0 THEN
        RAISE EXCEPTION 'Phase 3 gate failed: % sandbox_warm_pools rows still carry a non-empty concurrency_style; migrate concurrent-mode pools before dropping the column', remaining;
    END IF;
END $$;
ALTER TABLE sandbox_warm_pools
    DROP COLUMN IF EXISTS concurrency_style;

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
