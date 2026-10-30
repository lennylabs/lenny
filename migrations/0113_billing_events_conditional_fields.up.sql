-- §11.2.1 — Event schema (all events): the event-type-specific
-- ("(for X events only)") conditional fields. They are stored as a
-- single nullable JSONB blob rather than ~30 sparse columns: the
-- §11.2.1 null/absent field contract is exactly a sparse map keyed by
-- the field names that apply to a given event_type, and JSONB matches
-- the "Parquet optional columns" guidance the spec gives analytics
-- consumers. NULL on every event type that carries no event-type-
-- specific data (session.created, session.completed,
-- token_usage.checkpoint, billing_correction).
--
-- This migration is purely additive: the column is nullable, so existing
-- INSERT statements that do not name it continue to work, and the §11.7
-- lenny_billing_immutability trigger is unaffected — a billing event is
-- an INSERT, which the trigger already permits. No UPDATE or DELETE grant
-- is added; the append-only integrity model is preserved. F-11.2.12.
ALTER TABLE billing_events
    ADD COLUMN conditional_fields JSONB;
