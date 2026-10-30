DROP INDEX IF EXISTS idx_billing_events_labels;
ALTER TABLE billing_events DROP COLUMN IF EXISTS labels;
