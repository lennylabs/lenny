-- §25.5 brings the ops_event_subscriptions registry to the full webhook
-- subscription column set and adds the ops_event_deliveries
-- delivery-tracking table. Migration 0046 created the minimal v1 stub
-- (JSONB types, plaintext secret); this migration converts it to the
-- §25.5 schema: secret_hash + secret_fingerprint at rest (no plaintext),
-- the tenant-isolation columns (created_by, created_by_tenant_id,
-- tenant_filter), the generation counter for cache invalidation, and the
-- severity filter. Both tables are platform-scoped (no RLS, no tenant
-- guard); the tenant_filter column carries the §25.5 isolation scope.
--
-- The table is empty pre-deployment, so the stub `types` (JSONB) and
-- `secret` columns are dropped and `types` is rebuilt as the spec's
-- TEXT[] rather than cast in place.
--
-- phase3: not-required (ops_event_subscriptions is empty in every
-- deployment. The stub was created by migration 0046 in the same
-- unreleased line, so this DROP COLUMN is an empty-table reshape rather
-- than a §10.5 contract drop. The un-migrated-rows preflight gate has no
-- rows to count, so it does not apply.)
--
-- spec: §25.5 lines 2613-2664.

ALTER TABLE ops_event_subscriptions
    DROP COLUMN secret,
    DROP COLUMN types;

ALTER TABLE ops_event_subscriptions
    ADD COLUMN types                       TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN severity                    TEXT[],
    ADD COLUMN secret_hash                 TEXT NOT NULL DEFAULT '',
    ADD COLUMN secret_fingerprint          TEXT NOT NULL DEFAULT '',
    ADD COLUMN previous_secret_fingerprint TEXT,
    ADD COLUMN secret_rotated_at           TIMESTAMPTZ,
    ADD COLUMN description                 TEXT,
    ADD COLUMN created_by                  TEXT NOT NULL DEFAULT '',
    ADD COLUMN created_by_tenant_id        TEXT,
    ADD COLUMN tenant_filter               TEXT NOT NULL DEFAULT '*',
    ADD COLUMN generation                  BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN active                      BOOLEAN NOT NULL DEFAULT true;

CREATE TABLE ops_event_deliveries (
    id              BIGSERIAL PRIMARY KEY,
    subscription_id TEXT NOT NULL REFERENCES ops_event_subscriptions(id) ON DELETE CASCADE,
    event_id        TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    status          TEXT NOT NULL,
    attempts        INT NOT NULL DEFAULT 0,
    last_attempt_at TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX ops_event_deliveries_expires_at
    ON ops_event_deliveries (expires_at);
CREATE INDEX ops_event_deliveries_subscription_status
    ON ops_event_deliveries (subscription_id, status);
