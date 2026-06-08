-- §14 line 106 — session labels are filterable in GET /v1/metering/events
-- (the §11.2.1 billing event stream). Each billing event denormalizes the
-- session's §14 `labels` map so a label-scoped metering query filters in
-- SQL, preserving the §15.1 cursor/hasMore pagination contract, without
-- re-joining the session row. Stored as a nullable JSONB column: NULL
-- means the event carries no labels, which a non-empty `labels @> ...`
-- containment filter never matches. A GIN index backs the containment
-- filter. Purely additive: the column is nullable, so existing INSERT
-- statements that do not name it continue to work, and the §11.7
-- lenny_billing_immutability trigger is unaffected (a billing event is an
-- INSERT, which the trigger permits). F-14.1.13.
ALTER TABLE billing_events
    ADD COLUMN IF NOT EXISTS labels JSONB;

CREATE INDEX IF NOT EXISTS idx_billing_events_labels
    ON billing_events USING GIN (labels);
