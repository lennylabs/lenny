-- §12.5 line 313 / 326: in concurrent-workspace mode checkpoints are
-- per-slot — "the 'latest 2' limit applies independently to each
-- slot" and "the GC job and retention policy operate on
-- (session_id, slot_id) pairs, not on sessions alone." Migration 0067
-- keyed the rotation catalog on (tenant_id, session_id) only, so a
-- concurrent-workspace pod running several slots under one session
-- would have all but the latest two checkpoints rotated across the
-- whole session rather than per slot.
--
-- slot_id carries the §5.2 concurrent-mode slot identifier. The
-- single-workspace (session / task / stateless) checkpoint path
-- writes the empty string, so its rotation stays per-session; the
-- concurrent-workspace path writes the bound slot id so each slot
-- retains its own latest two.
--
-- spec: §12.5 lines 313, 326.

ALTER TABLE session_checkpoints
    ADD COLUMN slot_id TEXT NOT NULL DEFAULT '';

-- Replace the rotation index so the rotation worker can walk a single
-- (tenant, session, slot) pair's checkpoints by descending created_at
-- and identify that slot's latest two independently.
DROP INDEX IF EXISTS idx_session_checkpoints_session_age;
CREATE INDEX idx_session_checkpoints_slot_age
    ON session_checkpoints (tenant_id, session_id, slot_id, created_at DESC);
