-- The §12.5 artifact_store catalog table. Mirrors the MinIO blob
-- objects with their tenant, session, lifecycle state, and the
-- §12.5 soft-delete tombstone. The §12.5 GC sweep walks the
-- catalog to find expired or tombstoned objects to hard-prune.
--
-- The catalog is platform-global (no RLS) — the §12.5 control
-- plane runs background workers per tenant against this table, and
-- the tenant_id column is a denormalized convenience set on insert.
-- All cross-tenant reads carry the `-- platform-admin-cross-tenant-allowed`
-- annotation enforced by the §12.3 schema linter.

CREATE TABLE artifact_store (
    -- uri is the canonical blob URI string ("lenny-blob://{tenant}/{session}/{part}/...?...").
    uri                  TEXT        PRIMARY KEY,
    tenant_id            TEXT        NOT NULL REFERENCES tenants(id),
    session_id           TEXT        NOT NULL,
    part_id              TEXT        NOT NULL,
    mime_type            TEXT        NOT NULL DEFAULT '',
    size_bytes           BIGINT      NOT NULL DEFAULT 0,
    -- §12.5 lifecycle state: 'live', 'soft_deleted', or 'tombstoned'.
    state                TEXT        NOT NULL DEFAULT 'live',
    -- §12.5 SSE-KMS key alias the object was sealed with; NULL when
    -- the bucket-default SSE-S3 key was used.
    kms_key_alias        TEXT,
    -- §12.5 legal hold flag — when true the §12.8 erasure
    -- orchestrator refuses to remove the row.
    legal_hold           BOOLEAN     NOT NULL DEFAULT false,
    -- §12.5 soft-delete instant; NULL while the object is live.
    soft_deleted_at      TIMESTAMPTZ,
    -- §12.5 tombstone retention deadline; the GC sweep hard-prunes
    -- a tombstoned row whose deadline has passed.
    tombstone_deadline   TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT artifact_store_state_check
        CHECK (state IN ('live', 'soft_deleted', 'tombstoned'))
);

-- §12.5 GC sweep: walk by (state, deadline) to find tombstoned
-- objects that have passed their retention window.
CREATE INDEX idx_artifact_store_gc
    ON artifact_store (state, tombstone_deadline)
    WHERE state = 'tombstoned';

-- Per-tenant listing for the §12.8 erasure orchestrator.
CREATE INDEX idx_artifact_store_tenant_session
    ON artifact_store (tenant_id, session_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON artifact_store TO lenny_app;
