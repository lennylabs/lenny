-- §14 line 106 — session labels are filterable in GET /v1/usage (usage
-- reports). The usage accumulator denormalizes the session's §14 `labels`
-- map onto each usage event so a label-scoped usage report is computed in
-- SQL without re-joining the (eventually-erased) session row. Stored as a
-- nullable JSONB column: NULL means the event carries no labels, which a
-- non-empty `labels @> ...` containment filter never matches. A GIN index
-- backs the containment filter. Purely additive: the column is nullable,
-- so existing INSERT statements that do not name it continue to work and
-- the lenny_tenant_guard trigger is unaffected. F-14.1.13.
ALTER TABLE usage_events
    ADD COLUMN IF NOT EXISTS labels JSONB;

CREATE INDEX IF NOT EXISTS idx_usage_events_labels
    ON usage_events USING GIN (labels);
