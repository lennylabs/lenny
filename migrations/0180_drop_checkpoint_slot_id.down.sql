-- Reverse 0180: re-add the two slot_id columns at their original
-- definitions, restore the three indexes that carried the slot dimension,
-- and put the retired pod-global workspace root back on every session row
-- whose value is the slot root this migration derived for it.
--
-- spec: §4.9, §6.4, §7.3, §10.1, §12.5.

-- sessions carries the §12.3 tenant-guard trigger, so the reverse rewrite
-- takes the same platform cross-tenant sentinel and opt-in the forward
-- migration uses. Both are SET LOCAL and end with this transaction.
SET LOCAL lenny.allow_all_sentinel = 'true';
SET LOCAL app.current_tenant = '__all__';
UPDATE sessions
    SET workspace_root = '/workspace/current'
    WHERE workspace_root = '/workspace/slots/' || id::text || '/current';
SET LOCAL app.current_tenant TO DEFAULT;
SET LOCAL lenny.allow_all_sentinel TO DEFAULT;

DROP INDEX IF EXISTS idx_checkpoint_manifest_active;
DROP INDEX IF EXISTS partial_manifest_active_uniq;
ALTER TABLE checkpoint_manifest
    ADD COLUMN IF NOT EXISTS slot_id TEXT NOT NULL DEFAULT 'default';
CREATE UNIQUE INDEX partial_manifest_active_uniq
    ON checkpoint_manifest (session_id, slot_id)
    WHERE partial = TRUE AND deleted_at IS NULL;
CREATE INDEX idx_checkpoint_manifest_active
    ON checkpoint_manifest (tenant_id, session_id, slot_id, coordination_generation DESC)
    WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_session_checkpoints_session_age;
ALTER TABLE session_checkpoints
    ADD COLUMN IF NOT EXISTS slot_id TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_session_checkpoints_slot_age
    ON session_checkpoints (tenant_id, session_id, slot_id, created_at DESC);
