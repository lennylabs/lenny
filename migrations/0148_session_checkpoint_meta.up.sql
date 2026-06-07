-- §10.1 lines 178-179: the CheckpointBarrier protocol persists the
-- last completed tool call and its idempotency-key sequence so that a
-- new coordinator, after a rolling-update handoff, can deduplicate
-- tool-call re-dispatch and avoid silently re-executing an
-- idempotent-unsafe operation that the prior pod already ran.
--
-- The MinIO checkpoint manifest carries `barrier_meta.last_tool_call_id`
-- as the primary durable source (written by the adapter before it emits
-- CheckpointBarrierAck). This table is the secondary fast-lookup source
-- the gateway writes after receiving the ack (§10.1 line 178 (b)); the
-- resume path reads it first and only falls back to the manifest when
-- the row is absent because the prior gateway could not write it before
-- SIGKILL (§10.1 line 179).
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
-- spec: §10.1 lines 178-181, 393.

CREATE TABLE session_checkpoint_meta (
    tenant_id                   TEXT        NOT NULL REFERENCES tenants(id),
    session_id                  TEXT        NOT NULL,
    -- coordination_generation is the coordinator's fenced generation at
    -- the time the BarrierAck was received. A new coordinator that has
    -- already incremented past this value treats the row as authored by
    -- a stale generation when reasoning about (coordination, recovery)
    -- tuples, but the resume-dedup comparison itself keys on
    -- last_tool_call_sequence rather than the generation.
    coordination_generation     BIGINT      NOT NULL DEFAULT 0,
    -- barrier_id is the gateway's monotonically-increasing-per-session
    -- correlation id for the barrier that produced this row (§10.1 line
    -- 165). Stored so the next barrier on the same session can advance
    -- the counter across a coordinator handoff.
    barrier_id                  TEXT        NOT NULL DEFAULT '',
    -- last_tool_call_id is the id of the last tool call the pod
    -- completed before quiescing, echoed in the CheckpointBarrierAck
    -- (§10.1 line 166).
    last_tool_call_id           TEXT        NOT NULL DEFAULT '',
    -- last_tool_call_sequence is the tool_call_sequence_number component
    -- of the §10.1 line 178 tool_call_idempotency_key. The resume path
    -- skips any tool call whose sequence number is <= this value (§10.1
    -- line 179).
    last_tool_call_sequence     BIGINT      NOT NULL DEFAULT 0,
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
