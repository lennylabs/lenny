DROP INDEX IF EXISTS idx_usage_events_labels;
ALTER TABLE usage_events DROP COLUMN IF EXISTS labels;
