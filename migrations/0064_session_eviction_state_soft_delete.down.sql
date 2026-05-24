-- Reverse of 0064_session_eviction_state_soft_delete.up.sql.

DROP INDEX IF EXISTS idx_session_eviction_state_deleted_at;

ALTER TABLE session_eviction_state
    DROP COLUMN IF EXISTS deleted_at;
