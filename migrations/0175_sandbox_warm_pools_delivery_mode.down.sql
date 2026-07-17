-- Reverse 0175: drop the §4.9 credential-delivery combination columns. The
-- up is an append-only ALTER TABLE, so the reversal is a clean column drop
-- with no data backfill to undo.
ALTER TABLE sandbox_warm_pools
    DROP COLUMN IF EXISTS delivery_mode,
    DROP COLUMN IF EXISTS spiffe_binding,
    DROP COLUMN IF EXISTS allow_direct_mode_standard_isolation,
    DROP COLUMN IF EXISTS allow_proxy_mode_spiffe_binding_disabled;
