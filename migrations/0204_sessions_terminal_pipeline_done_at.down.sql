-- Reverse 0204: drop the §7.1 pipeline mark column. The up adds a nullable
-- column with no backfill, so the reversal has no data to restore.
ALTER TABLE sessions
    DROP COLUMN IF EXISTS terminal_pipeline_done_at;
