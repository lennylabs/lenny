-- spec: §10.5 Phase 3 (contract) — "Drop old columns/tables in a
-- subsequent release." Every Phase 3 migration file "must begin with
-- a preflight verification block that the migration runner executes
-- before applying any DDL. The runner issues the count query
-- `SELECT COUNT(*) FROM <table> WHERE <old_column> IS NOT NULL` (or an
-- equivalent expression capturing un-migrated rows ...) and aborts the
-- migration with a non-zero exit code if the result is nonzero" — here
-- the equivalent expression is `price_usd_cents IS NULL`, since a row
-- that was never dual-written or backfilled onto the new column has
-- not completed the Phase 2 migrate-reads step.
-- gate-index: idx_widgets_price_usd_cents_null
CREATE INDEX IF NOT EXISTS idx_widgets_price_usd_cents_null
    ON widgets (price_usd_cents)
    WHERE price_usd_cents IS NULL;
DO $$
DECLARE remaining bigint;
BEGIN
    SELECT COUNT(*) INTO remaining FROM widgets WHERE price_usd_cents IS NULL;
    IF remaining > 0 THEN
        RAISE EXCEPTION 'Phase 3 gate failed: % un-migrated rows remain in widgets.price_cents. Resolve data migration before retrying.', remaining;
    END IF;
END $$;
DROP INDEX IF EXISTS idx_widgets_price_usd_cents_null;
-- spec: §10.5 idempotency requirement — "Phase 3 migrations
-- (`ALTER TABLE ... DROP COLUMN`) are not idempotent — `DROP COLUMN`
-- fails if the column does not exist. Phase 3 migrations must use
-- `DROP COLUMN IF EXISTS` or guard with a pre-check."
ALTER TABLE widgets
    DROP COLUMN IF EXISTS price_cents,
    ALTER COLUMN price_usd_cents SET NOT NULL;
