-- Reverses 0034_usage_events. Dropping the table cascades its
-- idx_usage_events_tenant_created index, lenny_tenant_guard trigger,
-- RLS policy, and lenny_app grants.
DROP TABLE IF EXISTS usage_events;
