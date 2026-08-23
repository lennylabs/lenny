-- Reverse 0180: re-add the two slot_id columns at their original
-- definitions and restore the three indexes that carried the slot dimension.
--
-- sessions.workspace_root is left as it stands. A down migration cannot tell
-- a row the forward rewrite produced from a row that already recorded its own
-- slot root before the migration ran, so reversing the rewrite by predicate
-- would put the retired pod-global path onto sessions whose value was correct
-- both before and after. The recorded root is re-derived from the base the
-- adapter reports and re-persisted at the next handshake, so leaving it alone
-- costs nothing (§6.4, §7.3 step (d)).
--
-- The forward file's uniqueness gate reads without writing, so there is
-- nothing on that side to reverse. The widened isolation policy that read
-- needs is confined to the forward file, which restores the strict form
-- migration 0178 created before it finishes, so this file has no policy to
-- put back.
--
-- spec: §6.4, §7.3, §10.1, §12.5.

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
