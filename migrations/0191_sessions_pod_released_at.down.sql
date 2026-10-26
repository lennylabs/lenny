-- Reverse 0191: drop the terminal-arm index and the §7.1 release marker
-- column. The up adds a nullable column and an index with no backfill, so
-- the reversal has no data to restore.
DROP INDEX IF EXISTS idx_sessions_unreleased_terminal_by_pod;

ALTER TABLE sessions
    DROP COLUMN IF EXISTS pod_released_at;
