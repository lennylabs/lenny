-- Reverse 0170: drop the §4.7 / §5.3 pool-level nonce-only-mode
-- acknowledgment flag. The up is an append-only ALTER TABLE, so the
-- reversal is a clean column drop with no data backfill to undo.
ALTER TABLE sandbox_warm_pools
    DROP COLUMN IF EXISTS acknowledge_nonce_only_auth;
