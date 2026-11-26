-- §7.1 shared upload-token signing keys and the consumed state.
--
-- platform_signing_keys holds the signing keys that every gateway replica
-- shares, one row per key, grouped by purpose. The purpose upload-token
-- holds the HMAC-SHA256 keys of the uploadToken. wrapped_secret is the
-- secret envelope-encrypted under the purpose's platform KMS alias
-- (pkg/kms/envelope), so the plaintext exists only in gateway memory.
-- public_key is set only for an asymmetric algorithm. rotation_slot numbers
-- a purpose's keys in rotation order; the unique (purpose, rotation_slot)
-- constraint makes concurrent writers of the next key produce one key. A
-- key's retire_after is the next key's active_from and its delete_after is
-- retire_after plus the purpose's verification time; both stay NULL while
-- the key is the newest one. A key serves every tenant, so the table is
-- platform-global: no tenant_id column, no lenny_tenant_guard trigger, and
-- no RLS.
--
-- sessions.upload_token_consumed_at records the single-use invalidation of
-- the session's uploadToken. The FinalizeWorkspace finalizing -> ready
-- write sets it in the same update, and every replica reads it when it
-- verifies a token. A NULL column means the token is not consumed.
--
-- spec: §7.1 (uploadToken format and security properties), §12.3
-- (Platform signing keys), §12.9.
--
-- platform-global
CREATE TABLE platform_signing_keys (
    purpose        TEXT        NOT NULL,
    key_id         TEXT        NOT NULL,
    algorithm      TEXT        NOT NULL,
    wrapped_secret BYTEA       NOT NULL,
    public_key     BYTEA,
    kek_alias      TEXT        NOT NULL,
    rotation_slot  BIGINT      NOT NULL CHECK (rotation_slot >= 1),
    active_from    TIMESTAMPTZ NOT NULL,
    retire_after   TIMESTAMPTZ,
    delete_after   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (purpose, key_id),
    CONSTRAINT platform_signing_keys_slot_unique UNIQUE (purpose, rotation_slot)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON platform_signing_keys TO lenny_app;

ALTER TABLE sessions ADD COLUMN upload_token_consumed_at TIMESTAMPTZ;
