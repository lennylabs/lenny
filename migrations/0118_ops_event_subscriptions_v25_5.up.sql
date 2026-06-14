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
-- The stub `types` (JSONB) and `secret` columns are dropped and `types` is
-- rebuilt as the spec's TEXT[]. The reshape replaces both columns rather than
-- casting in place.
--
-- §10.5 Phase 3 column drop (spec §10.5 line 417). The DROP COLUMN is
-- irreversible, so it is fronted by a PL/pgSQL DO $$ preflight gate that counts
-- un-migrated rows and RAISE EXCEPTIONs when any remain. The reshape carries no
-- per-row backfill of the stub `secret`/`types` values into the new
-- secret_hash/secret_fingerprint/TEXT[] columns, so any existing
-- ops_event_subscriptions row is un-migrated data the drop would lose. The gate
-- fails closed on any such row. The whole up-file runs in one transaction, so a
-- RAISE EXCEPTION rolls back the entire migration. The drops are idempotent
-- (DROP COLUMN IF EXISTS) so a re-run after the gate passes is a no-op.
--
-- spec: §25.5 lines 2613-2664, §10.5 line 417 (Phase 3 enforcement gate).
-- gate-index: ops_event_subscriptions_pkey
DO $$
DECLARE remaining bigint;
BEGIN
    SELECT COUNT(*) INTO remaining FROM ops_event_subscriptions;
    IF remaining > 0 THEN
        RAISE EXCEPTION 'Phase 3 gate failed: % un-migrated rows remain in ops_event_subscriptions (the §25.5 reshape backfills no stub secret/types values). Resolve data migration before retrying.', remaining;
    END IF;
END $$;
ALTER TABLE ops_event_subscriptions
    DROP COLUMN IF EXISTS secret,
    DROP COLUMN IF EXISTS types;

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
