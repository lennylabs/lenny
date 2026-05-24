-- §4.4 line 234 references §12.5 "latest 2 checkpoints" retention.
-- The Checkpointer previously persisted only the latest
-- workspace_snapshot_ref on the sessions row, so prior checkpoint
-- blobs in MinIO accumulated without bound until manual cleanup. The
-- spec mandates the gateway track a per-session checkpoint catalog
-- so a rotation worker can retain only the latest two and mark older
-- entries soft-deleted; the §12.5 GC sweep then hard-prunes the
-- soft-deleted entries on the same tombstone retention lifecycle as
-- every other artifact.
--
-- The table is tenant-scoped and carries the standard §12.3 RLS
-- guard (lenny_tenant_guard trigger + lenny_tenant_isolation policy).
-- A composite key (tenant_id, session_id, ref) makes a checkpoint
-- ref unique per session; created_at orders the rotation, and
-- retained flags the latest two. deleted_at is the §4.4 line 236
-- soft-delete tombstone.
--
-- spec: §4.4 line 234, §12.5 latest-2-checkpoints retention.

CREATE TABLE session_checkpoints (
    tenant_id   TEXT        NOT NULL REFERENCES tenants(id),
    session_id  TEXT        NOT NULL,
    -- ref is the MinIO object reference for this checkpoint snapshot.
    -- Each successful checkpoint commit inserts a row keyed by
    -- (tenant_id, session_id, ref). A duplicate ref (idempotent
    -- replay of the same checkpoint) is rejected at the primary-key
    -- level.
    ref         TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- retained marks the rows that represent the latest two
    -- checkpoints for this session. The rotation worker sets older
    -- rows' retained=false in the same transaction that inserts the
    -- new row, so a sweep can range-scan the table by retained=false
    -- and issue the soft-delete sweep without re-reading the full
    -- table.
    retained    BOOLEAN     NOT NULL DEFAULT TRUE,
    -- deleted_at is the §4.4 line 236 soft-delete tombstone. The
    -- rotation worker stamps it when retained drops to false (so
    -- stale-leader retries see rows_affected = 0 on the second
    -- writer), and the §12.5 backstop sweep prunes rows older than
    -- the tombstone retention window.
    deleted_at  TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, session_id, ref)
);

-- Rotation index: walk a session's checkpoints by descending
-- created_at so the rotation worker can identify the latest two and
-- mark the rest with retained=false.
CREATE INDEX idx_session_checkpoints_session_age
    ON session_checkpoints (tenant_id, session_id, created_at DESC);

-- Backstop sweep index: range-scan soft-deleted rows by deleted_at
-- so the §12.5 hard-prune sweep can walk the tail without a full
-- table scan.
CREATE INDEX idx_session_checkpoints_deleted_at
    ON session_checkpoints (deleted_at)
    WHERE deleted_at IS NOT NULL;

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON session_checkpoints
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE session_checkpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_checkpoints FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON session_checkpoints
    USING (tenant_id = current_setting('app.current_tenant', false));

GRANT SELECT, INSERT, UPDATE, DELETE ON session_checkpoints TO lenny_app;
