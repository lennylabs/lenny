-- Reverse migration 0205: restore the (tenant_id, idempotency_key)
-- primary key. Rows of different principals may share a key string, so
-- the cache is discarded before the narrower key is restored.

TRUNCATE idempotency_keys;

ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE idempotency_keys
    ADD CONSTRAINT idempotency_keys_pkey
    PRIMARY KEY (tenant_id, idempotency_key);

ALTER TABLE idempotency_keys
    DROP COLUMN session_id,
    DROP COLUMN subject;
