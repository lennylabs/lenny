-- Reverses 0043_billing_correction_and_stream_dedup: drops the §11.2.1
-- billing-correction columns and the failover stream deduplication
-- column and indexes.

DROP INDEX IF EXISTS idx_billing_events_corrects;
DROP INDEX IF EXISTS idx_billing_events_stream_entry;

ALTER TABLE billing_events
    DROP COLUMN IF EXISTS stream_entry_id,
    DROP COLUMN IF EXISTS pod_minutes,
    DROP COLUMN IF EXISTS correction_detail,
    DROP COLUMN IF EXISTS correction_reason_code,
    DROP COLUMN IF EXISTS corrects_sequence;
