-- Reverses 0005_idempotency_keys. Dropping the table cascades its
-- stored_at index, lenny_tenant_guard trigger, RLS policy, and
-- lenny_app grants.
DROP TABLE IF EXISTS idempotency_keys;
