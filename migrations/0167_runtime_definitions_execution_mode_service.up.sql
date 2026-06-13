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
-- (session, service) set. The execution_mode columns on these tables
-- carry no CHECK constraint; only the comment named the old enum.
COMMENT ON COLUMN sandbox_warm_pools.execution_mode IS 'the §5.2 mode (session, service)';
COMMENT ON COLUMN sessions.execution_mode IS 'the §5.2 mode (session, service)';

-- Retire the concurrency_style column from the pool table. The
-- (session, service) model has no concurrent sub-variant; max_concurrent
-- survives as the per-pod bound consumed by service mode and the
-- session-mode concurrency path (sessionPolicy.maxConcurrentSessions).
ALTER TABLE sandbox_warm_pools
    DROP COLUMN concurrency_style;

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
