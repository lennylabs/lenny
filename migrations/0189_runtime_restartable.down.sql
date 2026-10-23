-- Reverse 0189: drop the §5.1 derived restartable mirror column. The up
-- is an append-only ALTER TABLE, so the reversal is a clean column drop
-- with no data backfill to undo.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS restartable;
