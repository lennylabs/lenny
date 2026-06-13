-- §5.2 / §12.6 execution-mode rename: the v1 mode set collapses from
-- (session, task, concurrent) to (session, service). This migration
-- re-keys every Postgres surface that enumerated the old mode set and
-- adds the gateway-written per-pod recycle counters to agent_pod_state.
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

-- Re-key the stale unconstrained-column mode comments to the
-- (session, service) set. These columns carry no CHECK constraint; only
-- the comment named the old mode enum.
--   - sandbox_warm_pools.execution_mode: the 0033:15 source comment.
--   - sessions.execution_mode: the 0084:10-11 source comment.
--   - sessions.scrub_policy: the 0084:15-19 source comment, whose gating
--     clause at 0084:18 named the removed pod-reuse mode set. In the
--     (session, service) model the scrub policy applies to service-mode
--     pod reuse, so the gating clause re-keys to service mode.
COMMENT ON COLUMN sandbox_warm_pools.execution_mode IS 'the §5.2 mode (session, service)';
COMMENT ON COLUMN sessions.execution_mode IS 'the §5.2 mode (session, service)';
COMMENT ON COLUMN sessions.scrub_policy IS 'the §7.1 scrub-policy string; set only when execution_mode is service (the pod-reuse mode)';

-- Retire the concurrency_style column from the pool table. The
-- (session, service) model has no concurrent sub-variant; max_concurrent
-- survives as the per-pod bound consumed by service mode and the
-- session-mode concurrency path (sessionPolicy.maxConcurrentSessions).
--
-- Dropping concurrency_style is a Phase 3 (contract) step, so it is
-- fronted by the §10.5 preflight gate: the DO block aborts the migration
-- when any pool still carries a non-default concurrency_style (an
-- un-retired concurrent sub-variant), and the DROP runs idempotently
-- (DROP COLUMN IF EXISTS) so a re-run after a partial failure succeeds.
-- spec: §10.5 (Phase 3 enforcement gate, idempotency).
-- gate-index: idx_sandbox_warm_pools_concurrency_style_partial
DO $$
DECLARE remaining bigint;
BEGIN
    SELECT COUNT(*) INTO remaining
        FROM sandbox_warm_pools
        WHERE concurrency_style <> '';
    IF remaining > 0 THEN
        RAISE EXCEPTION 'Phase 3 gate failed: % un-migrated rows remain in sandbox_warm_pools.concurrency_style. Resolve data migration before retrying.', remaining;
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
