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
-- pkg/gateway/poolstore ConcurrencyStyle field, enum, and ValidateConcurrentConfig
-- removal so no live code reads a column the migration has dropped. That
-- Go-side removal is a later poolstore mode-collapse step, so the column drop
-- lands there with it, not here. Dropping the column now would leave the
-- still-live concurrent-mode validatePool -> ValidateConcurrentConfig path
-- reading a zero-valued ConcurrencyStyle off a re-read row and rejecting it.
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
