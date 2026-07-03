-- §10.1: the CheckpointBarrier protocol persists per-session barrier
-- metadata: the barrier_id, the checkpoint_ref the barrier flush
-- produced, and the workspace_recovery_fraction. The gateway writes a
-- row after receiving a CheckpointBarrierAck during a graceful drain
-- (§10.1 line 166 records the barrier_id in the session's checkpoint
-- metadata).
--
-- One row per session holds the latest barrier metadata: the
-- BarrierAck handler upserts on every barrier, overwriting the prior
-- generation's row. `workspace_recovery_fraction` is the §10.1 line 393
-- value the `session.resumed{resumeMode: coordinator_handoff}` event is
-- sourced from.
--
-- The table is tenant-scoped and carries the same RLS posture every
-- §12.3 tenant-scoped table uses: the `lenny_tenant_guard` trigger
-- fires on every write, RLS filters through
-- `current_setting('app.current_tenant', false)`, and `lenny_app` gets
-- the standard grants.
--
-- spec: §10.1 lines 165-166, 393.

CREATE TABLE session_checkpoint_meta (
    tenant_id                   TEXT        NOT NULL REFERENCES tenants(id),
    session_id                  TEXT        NOT NULL,
    -- coordination_generation is the coordinator's fenced generation at
    -- the time the BarrierAck was received. A new coordinator that has
    -- already incremented past this value treats the row as authored by
    -- a stale generation when reasoning about (coordination, recovery)
    -- tuples.
    coordination_generation     BIGINT      NOT NULL DEFAULT 0,
    -- barrier_id is the gateway's monotonically-increasing-per-session
    -- correlation id for the barrier that produced this row (§10.1 line
    -- 165). Stored so the next barrier on the same session can advance
    -- the counter across a coordinator handoff.
    barrier_id                  TEXT        NOT NULL DEFAULT '',
    -- checkpoint_ref is the MinIO checkpoint manifest the barrier flush
    -- stored (empty when the best-effort flush produced no checkpoint).
    checkpoint_ref              TEXT        NOT NULL DEFAULT '',
    -- workspace_recovery_fraction is the §10.1 line 393 value the
    -- coordinator-handoff `session.resumed` event reports. NULL when the
    -- session had no prior full checkpoint to compute a fraction
    -- against; the event omits the field per §7.2 optional-fraction
    -- rules.
    workspace_recovery_fraction DOUBLE PRECISION,
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, session_id)
);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON session_checkpoint_meta
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE session_checkpoint_meta ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_checkpoint_meta FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON session_checkpoint_meta
    USING (tenant_id = current_setting('app.current_tenant', false));

GRANT SELECT, INSERT, UPDATE, DELETE ON session_checkpoint_meta TO lenny_app;
