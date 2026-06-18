-- Reverse 0169: drop the §4.7 / §5.1 nonce-only activation mirror
-- column. The up is an append-only ALTER TABLE, so the reversal is a
-- clean column drop with no data backfill to undo.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS require_so_peercred;
