-- §8.10 lines 129, 1062 / §7.1 lines 426-433 / §12.7 lines 783, 807.
-- session_tree_archive is the durable record of a delegation tree's
-- settled child results. When a child session reaches a terminal state
-- the gateway offloads its §8.8 TaskResult payload here, keyed by
-- (root_session_id, node_session_id), and replaces the in-memory node
-- with a lightweight stub. A resumed parent (coordinator handoff,
-- replica failover, or a parent returning from awaiting_client_action)
-- replays the archived results in original-settlement order so it
-- observes a complete and consistent view of every child outcome
-- regardless of when each settled relative to the parent's own pod
-- failure.
--
-- The FK on root_session_id -> sessions(id) has no ON DELETE action
-- (RESTRICT), which is what the §12.7 erasure ordering requires: the
-- archive MUST be deleted before its tree's sessions (line 807). A
-- CASCADE would let a sessions delete silently drop archive rows out of
-- the prescribed order. node_session_id and parent_session_id are bare
-- UUID columns without an FK because a node or its parent may be
-- retention-GC'd while the archived result must outlive it.
--
-- completion_seq is the §15.1 reattach predicate column: the
-- children_reattached synthesis streams archived nodes whose
-- completion_seq > resumeFromSeq, so the column must exist for that
-- query to bind. v1 writers default it to 0; the §10.4 reattach path
-- populates it.
--
-- The table is session-sharded: every node in a tree shares the
-- root's §12.6 routing prefix and co-locates on one shard, so a tree
-- replay is a single-shard query.
-- session-sharded-justification: keyed by root_session_id whose
--   embedded routing prefix every node in the delegation tree shares;
--   a tree replays from a single shard.
-- session-sharded
CREATE TABLE session_tree_archive (
    tenant_id         TEXT        NOT NULL REFERENCES tenants(id),
    root_session_id   UUID        NOT NULL REFERENCES sessions(id),
    node_session_id   UUID        NOT NULL,
    parent_session_id UUID,
    state             TEXT        NOT NULL,
    result            JSONB,
    settled_at        TIMESTAMPTZ NOT NULL,
    archived_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completion_seq    BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (root_session_id, node_session_id)
);

-- R-01 secondary index for the tenant scatter-gather and GDPR erasure
-- paths, leading with tenant_id per §12.5 shard-key index discipline.
CREATE INDEX idx_session_tree_archive_tenant
    ON session_tree_archive (tenant_id, root_session_id);

-- GetByNode resolves a settled child by its globally-unique node id
-- without knowing the tree root (the §8.8 re-archive read-modify-write
-- and the §15.1 single-node lookup both use it).
CREATE INDEX idx_session_tree_archive_node
    ON session_tree_archive (tenant_id, node_session_id);

ALTER TABLE session_tree_archive ENABLE ROW LEVEL SECURITY;

CREATE POLICY session_tree_archive_tenant_isolation
    ON session_tree_archive
    USING (tenant_id = current_setting('app.current_tenant', false));

-- §4.4 line 293 / §12.3 — the lenny_tenant_guard trigger rejects any
-- write whose transaction has not set app.current_tenant, so a bare
-- connection cannot bypass the RLS policy.
CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON session_tree_archive
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

-- §4.2 line 163 / §12.8 step 11 — the gateway connects as lenny_app and
-- archives, replays, and (for §12.8 erasure) deletes archive rows inside
-- a SET LOCAL app.current_tenant transaction. lenny_app therefore needs
-- full DML on the table; RLS plus the tenant guard keep each transaction
-- scoped to its own tenant. Without this grant the application role is
-- denied at the table level before the RLS policy is ever consulted.
GRANT SELECT, INSERT, UPDATE, DELETE ON session_tree_archive TO lenny_app;
