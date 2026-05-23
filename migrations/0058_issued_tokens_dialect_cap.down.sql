-- Rollback for 0058: drop the dialect-cap and multi-audience columns
-- and the gin index that backs the multi-audience lookup.

DROP INDEX IF EXISTS idx_issued_tokens_audiences;
ALTER TABLE issued_tokens
    DROP COLUMN IF EXISTS dialect_cap_applied_seconds,
    DROP COLUMN IF EXISTS audiences;
