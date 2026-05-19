-- §11.2.1 billing-correction columns and the failover stream
-- deduplication column.
--
-- This migration is purely additive: every new column is nullable or
-- carries a default, so existing INSERT statements that do not name the
-- new columns continue to work, and the §11.7 lenny_billing_immutability
-- trigger is unaffected — a billing_correction is an INSERT, which the
-- trigger already permits. No UPDATE or DELETE grant is added; the
-- append-only integrity model is preserved.

-- --- billing_correction columns -----------------------------------------
-- §11.2.1: a billing_correction event adjusts a previously emitted event
-- without violating append-only immutability. The correction carries its
-- own sequence_number and references the original event via
-- corrects_sequence; tokens_input / tokens_output / pod_minutes on the
-- correction row are the replacement values for the original.
ALTER TABLE billing_events
    -- corrects_sequence references the original event a correction
    -- adjusts. NULL on every non-correction event; sequence numbers
    -- start at 1, so a correction's value is always >= 1.
    ADD COLUMN corrects_sequence      BIGINT,
    -- correction_reason_code is the §11.2.1 structured reason code,
    -- required on a billing_correction and empty on every other event.
    ADD COLUMN correction_reason_code TEXT        NOT NULL DEFAULT '',
    -- correction_detail is the optional free-text supplement.
    ADD COLUMN correction_detail      TEXT        NOT NULL DEFAULT '',
    -- pod_minutes is the §11.2.1 wall-clock pod time dimension. It
    -- predates this migration as a schema field but had no column; a
    -- correction needs it because pod_minutes is one of the replaceable
    -- cost dimensions.
    ADD COLUMN pod_minutes            DOUBLE PRECISION NOT NULL DEFAULT 0;

-- --- Stream deduplication column ----------------------------------------
-- §11.2.1 two-tier failover: when a billing event is buffered through
-- the Redis stream during a Postgres outage, the flusher replays it with
-- INSERT ... ON CONFLICT (tenant_id, stream_entry_id) DO NOTHING. The
-- stream_entry_id stores the Redis stream entry id (e.g. 1680000000000-0)
-- so a reclaimed entry that a crashed consumer already inserted is a
-- no-op on the second INSERT. It is NULL for events written directly to
-- Postgres (the common, healthy path).
ALTER TABLE billing_events
    ADD COLUMN stream_entry_id TEXT;

-- The §11.2.1 per-tenant unique constraint that makes the ON CONFLICT
-- clause idempotent. A partial index excludes the NULL stream_entry_id
-- rows (events written directly to Postgres) so the constraint applies
-- only to stream-replayed events.
CREATE UNIQUE INDEX idx_billing_events_stream_entry
    ON billing_events (tenant_id, stream_entry_id)
    WHERE stream_entry_id IS NOT NULL;

-- An index on the correction reference so reconstructing the accurate
-- ledger (applying corrections to the events they reference) is a single
-- index probe rather than a scan.
CREATE INDEX idx_billing_events_corrects
    ON billing_events (tenant_id, corrects_sequence)
    WHERE corrects_sequence IS NOT NULL;
