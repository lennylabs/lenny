-- §4.2 Session record fields the spec lists but Phase 1.5 did not yet
-- carry: cwd, pod assignment, recovery_generation, coordination_generation,
-- and schema_version. See spec/04_system-components.md §4.2 line 156:
--
--   "Session records (id, tenant_id, user_id, state, pool, pod
--    assignment, cwd, recovery_generation, coordination_generation,
--    schema_version)."
--
-- recovery_generation is incremented on each pod recovery and is visible
-- to clients via the session API and `session.resumed` events.
-- coordination_generation is incremented on coordinator handoff across
-- gateway replicas and is the internal split-brain fencing counter. Both
-- counters are monotonically non-decreasing across every state
-- transition; the CHECK constraints below enforce the floor at the
-- column level. The default of zero matches the §4.2 invariant that a
-- newly created session has not yet been recovered or handed off.
--
-- pod_assignment is the persistent §4.2 pod-to-session binding. The
-- gateway's per-replica in-memory Registry is a hot cache; this column
-- is the source of truth across replicas and lets a fresh replica
-- recover the binding after a coordinator handoff per §7.2. An empty
-- string means the session is not currently bound to a pod (created
-- but not yet started, or already drained).
--
-- cwd is the §4.2 / §4.7 session working directory recorded for audit
-- reconstruction; the empty default leaves it unset until the runtime
-- adapter materialises the workspace.
--
-- schema_version is the row schema version. v1 sessions start at 1; the
-- column lets later migrations evolve the row layout without breaking
-- the gateway's read path.

ALTER TABLE sessions
    ADD COLUMN cwd                     TEXT   NOT NULL DEFAULT '',
    ADD COLUMN pod_assignment          TEXT   NOT NULL DEFAULT '',
    ADD COLUMN recovery_generation     BIGINT NOT NULL DEFAULT 0
        CHECK (recovery_generation >= 0),
    ADD COLUMN coordination_generation BIGINT NOT NULL DEFAULT 0
        CHECK (coordination_generation >= 0),
    ADD COLUMN schema_version          INT    NOT NULL DEFAULT 1;
