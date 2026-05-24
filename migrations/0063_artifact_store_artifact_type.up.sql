-- §4.4 line 291: storage quota accounting for eviction context objects.
-- The eviction-fallback writer (pkg/gateway/evictionfallback) records
-- an `artifact_store` row alongside every successfully-uploaded
-- eviction-context MinIO object so the bytes are tracked in the §12.5
-- GC catalog and counted against the per-tenant storage quota. The
-- spec mandates the row carry `artifact_type = eviction_context` so
-- the §12.5 GC sweep can drive the matching MinIO delete when the
-- session-level cleanup arrives.
--
-- This migration adds the artifact_type column to the artifact_store
-- catalog. The column is NOT NULL with a default of 'workspace'
-- (matching the existing v1 row population pattern — every catalog
-- row before this migration represents a workspace artifact). The
-- §4.4 eviction-fallback writer sets the column to 'eviction_context'
-- on its dedicated path; future artifact kinds (checkpoint chunks,
-- exported subsets) extend the enum without further schema changes.
--
-- spec: §4.4 line 291.

ALTER TABLE artifact_store
    ADD COLUMN IF NOT EXISTS artifact_type TEXT NOT NULL DEFAULT 'workspace';

-- Constrain the enum so future code paths cannot silently accept a
-- typo. The closed set tracks the spec-defined artifact kinds; new
-- kinds extend it as the storage-architecture evolves.
ALTER TABLE artifact_store
    DROP CONSTRAINT IF EXISTS artifact_store_artifact_type_check;

ALTER TABLE artifact_store
    ADD CONSTRAINT artifact_store_artifact_type_check
    CHECK (artifact_type IN ('workspace', 'eviction_context', 'checkpoint', 'export'));

-- §4.4 line 291 / §12.5 GC sweep: index on (artifact_type,
-- soft_deleted_at) so the eviction-context-specific cleanup queries
-- can range-scan the catalog without re-reading the entire table.
CREATE INDEX IF NOT EXISTS idx_artifact_store_type_state
    ON artifact_store (artifact_type, state);
