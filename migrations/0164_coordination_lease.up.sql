-- §10.1 lines 163-165 CheckpointBarrier barrier-target mirror. The
-- preStop graceful-drain barrier dispatches a CheckpointBarrier to every
-- session this gateway replica coordinates; §10.1 line 165 sources that
-- barrier-target set from a Postgres query against this mirror table of
-- the form
--
--   SELECT session_id FROM coordination_lease
--   WHERE coordinator_replica = $this_replica_id AND released_at IS NULL
--
-- (not the in-memory lease cache). Reading from Postgres observes
-- coordinator handoffs that occurred in the seconds before preStop:
-- the in-memory cache can both include stale entries for sessions just
-- handed off to another replica (false-positive barriers) and miss
-- entries for sessions just handed in (false-negatives that leave a
-- recently-dispatched tool call without barrier deduplication). The
-- coordination Sweeper mirrors the authoritative Redis lease into this
-- row on every sweep: it upserts the row with this replica as
-- coordinator_replica when it holds the lease, so a cross-replica
-- handoff overwrites coordinator_replica with the new holder, and it
-- marks released_at when the session reaches a terminal state.
--
-- This is platform-operational coordination state, not tenant data: a
-- single gateway replica coordinates sessions across every tenant, so
-- the barrier-target query is cross-tenant and there is no
-- lenny_tenant_guard trigger and no RLS policy (the same posture the
-- Redis lease store has, which carries no RLS either). GDPR erasure is
-- handled by the §12.1 mandatory DeleteByUser / DeleteByTenant
-- primitives on the store role interface, run by the §12.8 orchestrator
-- before the tenant row is deleted.
--
-- spec: §10.1 lines 163-181; §12.1 line 5 (mandatory erasure primitives).

CREATE TABLE coordination_lease (
    tenant_id               TEXT        NOT NULL,
    session_id              TEXT        NOT NULL,
    -- coordinator_replica is the OTel service.instance.id of the gateway
    -- replica that currently holds the session-coordination lease. The
    -- §10.1 line 165 barrier-target query filters on it.
    coordinator_replica     TEXT        NOT NULL,
    -- coordination_generation is the §4.2 fenced generation the holder
    -- last observed for the session. The CheckpointBarrier message
    -- carries it (§10.1 line 165); the adapter rejects a barrier whose
    -- generation does not match its last fenced value.
    coordination_generation BIGINT      NOT NULL DEFAULT 0,
    acquired_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- released_at is set when the session reaches a terminal state (the
    -- lease is no longer held by anyone). The barrier-target query
    -- excludes released rows; an unreleased row whose coordinator_replica
    -- names another replica is a handed-off session this replica no
    -- longer coordinates.
    released_at             TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, session_id)
);

-- The §10.1 line 165 barrier-target query is `WHERE coordinator_replica
-- = $1 AND released_at IS NULL`; a partial index on coordinator_replica
-- over the active rows keeps the drain-time fan-out read bounded under
-- the 2-second deadline.
CREATE INDEX coordination_lease_held_by_replica
    ON coordination_lease (coordinator_replica)
    WHERE released_at IS NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON coordination_lease TO lenny_app;
