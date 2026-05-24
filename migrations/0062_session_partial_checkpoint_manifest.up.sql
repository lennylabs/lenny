-- §4.4 lines 234 / 236: the partial-checkpoint manifest is the
-- recovery-aid row the gateway writes when an eviction checkpoint
-- exceeds the preStop tiered cap and the workspace upload is
-- incomplete. It is NOT a valid checkpoint — full checkpoints are
-- recorded against the sessions row instead — but it tracks the
-- prefix under which successfully-committed chunks live so the §7.2
-- resume path can attempt a partial reconstruction.
--
-- The table is tenant-scoped and carries the same RLS policy every
-- §12.3 tenant-scoped table uses: a SELECT/UPDATE/DELETE filters
-- through `current_setting('app.current_tenant', false)`, the
-- `lenny_tenant_guard` trigger fires on every write, and the
-- `lenny_app` role gets the standard grants.
--
-- v1 implements the §4.4 mandated fields the cleanup path needs:
-- (tenant_id, session_id, generation, partial_object_key_prefix,
-- chunk_encoding, created_at, deleted_at). The §10.1 partial-upload
-- pipeline (chunk_count, workspace_bytes_uploaded,
-- baseline_full_checkpoint_bytes, etc.) is a follow-on phase tracked
-- under §10.1 findings; the columns are deliberately omitted here so
-- the v1 schema does not advertise capabilities the rest of the code
-- path cannot yet honor.
--
-- spec: §4.4 lines 234, 236.

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
