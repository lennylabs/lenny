-- Reverse 0171: drop the §13.2 per-pool DNS opt-out column. The up is an
-- append-only ALTER TABLE, so the reversal is a clean column drop with no
-- data backfill to undo.
ALTER TABLE sandbox_warm_pools
    DROP COLUMN IF EXISTS dns_policy;
