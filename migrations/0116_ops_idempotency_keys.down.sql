-- Reverses 0116_ops_idempotency_keys.up.sql. The index drops with the table.
DROP INDEX IF EXISTS ops_idempotency_keys_expires_at;
DROP TABLE IF EXISTS ops_idempotency_keys;
