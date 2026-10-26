-- Reverse 0190: drop the §12.3 per-write identifier column. The up is an
-- append-only ALTER TABLE, so the reversal is a clean column drop with no
-- data backfill to undo.
ALTER TABLE sessions
    DROP COLUMN IF EXISTS last_write_id;
