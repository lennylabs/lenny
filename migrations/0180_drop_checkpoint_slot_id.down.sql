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
-- nothing on that side to reverse. Its cross-tenant read does need the
-- checkpoint_manifest isolation policy put back in the strict form migration
-- 0178 created it with, so the reverted schema admits no '__all__' read.
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

DROP POLICY IF EXISTS lenny_tenant_isolation ON checkpoint_manifest;
CREATE POLICY lenny_tenant_isolation ON checkpoint_manifest
    USING (tenant_id = current_setting('app.current_tenant', false));
