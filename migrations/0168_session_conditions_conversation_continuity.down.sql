-- Reverse 0168: drop the §7.1 conversation_continuity envelope half and
-- the §7.2 / §8.8 Terminated/Suspended session-condition columns. The
-- ALTER TABLE is append-only on the up side, so the reversal is a clean
-- column drop with no data backfill to undo.
ALTER TABLE sessions
    DROP COLUMN IF EXISTS conversation_continuity,
    DROP COLUMN IF EXISTS terminated_at,
    DROP COLUMN IF EXISTS terminated_reason,
    DROP COLUMN IF EXISTS suspended_at,
    DROP COLUMN IF EXISTS suspended_reason;
