-- Rollback for migration 0063. Drops the index, the CHECK
-- constraint, and the artifact_type column added for the §4.4 line
-- 291 eviction-context accounting path.

DROP INDEX IF EXISTS idx_artifact_store_type_state;

ALTER TABLE artifact_store
    DROP CONSTRAINT IF EXISTS artifact_store_artifact_type_check;

ALTER TABLE artifact_store
    DROP COLUMN IF EXISTS artifact_type;
