-- §11.5 idempotency records are scoped by the authenticated principal:
-- the tenant, the subject, and the session the bearer is bound to
-- (change 0178). The primary key becomes
-- (tenant_id, subject, session_id, idempotency_key).
--
-- The rows are a 24-hour response cache and no deployment exists, so
-- the migration discards them (TRUNCATE, which the row-level security
-- policy and the per-row tenant guard do not filter) rather than
-- backfilling a subject a row never recorded. session_id holds the
-- empty string for a bearer bound to no session, because a primary-key
-- column cannot be NULL.
--
-- The lenny_tenant_guard trigger, the row-level security policy, and
-- the (tenant_id, stored_at) index of migration 0082 are unchanged.

TRUNCATE idempotency_keys;

ALTER TABLE idempotency_keys
    ADD COLUMN subject    TEXT NOT NULL,
    ADD COLUMN session_id TEXT NOT NULL DEFAULT '';

ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE idempotency_keys
    ADD CONSTRAINT idempotency_keys_pkey
    PRIMARY KEY (tenant_id, subject, session_id, idempotency_key);
