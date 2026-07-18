-- Reverse of 0175: drop checkpoint_manifest (and with it its indexes,
-- trigger, and policy) and recreate the migration 0062
-- session_partial_checkpoint_manifest table and the migration 0150
-- partial unique index it superseded.
DROP POLICY IF EXISTS lenny_tenant_isolation ON checkpoint_manifest;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON checkpoint_manifest;
DROP INDEX IF EXISTS idx_checkpoint_manifest_deleted_at;
DROP INDEX IF EXISTS idx_checkpoint_manifest_active;
DROP INDEX IF EXISTS partial_manifest_active_uniq;
DROP TABLE IF EXISTS checkpoint_manifest;

CREATE TABLE session_partial_checkpoint_manifest (
    tenant_id                 TEXT        NOT NULL REFERENCES tenants(id),
    session_id                TEXT        NOT NULL,
    -- generation distinguishes successive partial manifests for the
    -- same (tenant, session) over the session lifetime — typically
    -- the session's recovery generation at the time the partial was
    -- captured; the resume path filters on the max generation under
    -- `deleted_at IS NULL` so a late-committed older-generation row
    -- cannot win against a fenced newer-generation writer (§10.1
    -- CPS-006 split-brain protection).
    generation                BIGINT      NOT NULL DEFAULT 0,
    -- partial_object_key_prefix is the MinIO prefix
    -- `/{tenant_id}/checkpoints/{session_id}/partial/{checkpoint_id}/`
    -- under which the chunks live. The resume / cleanup path keys
    -- off it directly so the value is captured at write time and
    -- never modified afterwards.
    partial_object_key_prefix TEXT        NOT NULL,
    -- chunk_encoding is `tar` or `tar.gz` — the wire encoding the
    -- adapter wrote the chunks with. The resume-time decode pipeline
    -- consults this column rather than inferring from the object-key
    -- suffix.
    chunk_encoding            TEXT        NOT NULL DEFAULT 'tar',
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- deleted_at is the §4.4 line 236 soft-delete tombstone. The
    -- cleanup path issues
    -- `UPDATE ... SET deleted_at = now() ... WHERE ... AND deleted_at IS NULL`
    -- so stale-leader retries / GC-backstop races converge to a
    -- single state mutation; the `artifact_store` tombstone sweep
    -- hard-prunes rows whose deleted_at is older than the retention
    -- window.
    deleted_at                TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, session_id, generation),
    CONSTRAINT session_partial_checkpoint_manifest_chunk_encoding_check
        CHECK (chunk_encoding IN ('tar', 'tar.gz'))
);

-- Resume / cleanup index: walk the active (non-soft-deleted) manifest
-- for a (tenant, session) by descending generation.
CREATE INDEX idx_session_partial_checkpoint_manifest_active
    ON session_partial_checkpoint_manifest (tenant_id, session_id, generation DESC)
    WHERE deleted_at IS NULL;

-- §12.5 backstop sweep: walk every soft-deleted row that has aged out
-- of the tombstone window so the sweep can hard-prune it in tandem
-- with `artifact_store`.
CREATE INDEX idx_session_partial_checkpoint_manifest_deleted_at
    ON session_partial_checkpoint_manifest (deleted_at)
    WHERE deleted_at IS NOT NULL;

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON session_partial_checkpoint_manifest
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE session_partial_checkpoint_manifest ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_partial_checkpoint_manifest FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON session_partial_checkpoint_manifest
    USING (tenant_id = current_setting('app.current_tenant', false));

GRANT SELECT, INSERT, UPDATE, DELETE ON session_partial_checkpoint_manifest TO lenny_app;

-- §10.1 lines 143-151: the migration 0150 at-most-one-active-partial-
-- manifest partial unique index, scoped on (tenant_id, session_id)
-- because the recreated table carries no slot_id column.
CREATE UNIQUE INDEX partial_manifest_active_uniq
    ON session_partial_checkpoint_manifest (tenant_id, session_id)
    WHERE deleted_at IS NULL;
