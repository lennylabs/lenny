-- Reverse migration 0082: restore the single-column stored_at index.

DROP INDEX IF EXISTS idempotency_keys_tenant_stored_at_idx;
CREATE INDEX idempotency_keys_stored_at_idx ON idempotency_keys (stored_at);
