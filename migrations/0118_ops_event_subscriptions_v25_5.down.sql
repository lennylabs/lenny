-- Reverse 0118: drop the delivery-tracking table and roll
-- ops_event_subscriptions back to the migration 0046 stub schema
-- (JSONB types, plaintext secret).

DROP INDEX IF EXISTS ops_event_deliveries_subscription_status;
DROP INDEX IF EXISTS ops_event_deliveries_expires_at;
DROP TABLE IF EXISTS ops_event_deliveries;

ALTER TABLE ops_event_subscriptions
    DROP COLUMN IF EXISTS severity,
    DROP COLUMN IF EXISTS secret_hash,
    DROP COLUMN IF EXISTS secret_fingerprint,
    DROP COLUMN IF EXISTS previous_secret_fingerprint,
    DROP COLUMN IF EXISTS secret_rotated_at,
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS created_by,
    DROP COLUMN IF EXISTS created_by_tenant_id,
    DROP COLUMN IF EXISTS tenant_filter,
    DROP COLUMN IF EXISTS generation,
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS active,
    DROP COLUMN IF EXISTS types;

ALTER TABLE ops_event_subscriptions
    ADD COLUMN types  JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN secret TEXT  NOT NULL DEFAULT '';
