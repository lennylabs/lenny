-- Down migration for 0112: drop the per-slot rotation dimension and
-- restore the per-session rotation index.

DROP INDEX IF EXISTS idx_session_checkpoints_slot_age;
CREATE INDEX idx_session_checkpoints_session_age
    ON session_checkpoints (tenant_id, session_id, created_at DESC);
ALTER TABLE session_checkpoints DROP COLUMN IF EXISTS slot_id;
