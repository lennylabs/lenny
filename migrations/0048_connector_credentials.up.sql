-- The §9.3 / §13.3 connector OAuth credential store.
--
-- connector_credentials persists the OAuth access + refresh tokens
-- the §9.3 connector OAuth flow obtains for an end-user. Tokens are
-- T4 Restricted per §12.9: every secret column is envelope-encrypted
-- with a per-tenant DEK wrapped by the per-tenant KMS KEK (alias
-- `tenant:{tenant_id}`), mirroring the §4.9 user-supplied credential
-- store (migration 0036 + 0039) field-for-field.
--
-- The table is tenant-scoped: lenny_tenant_guard rejects writes
-- without app.current_tenant, FORCE ROW LEVEL SECURITY scopes reads
-- under the per-tenant RLS policy, and the §4.2 platform-admin
-- `__all__` bypass (migration 0047) applies for cross-tenant
-- platform reads.

CREATE TABLE connector_credentials (
    tenant_id                  TEXT        NOT NULL REFERENCES tenants(id),
    -- connector_id names the §9.3 connector definition the credential
    -- belongs to. The (tenant, connector, user) triple is the primary
    -- key per the §9.3 ConnectorCredential record shape.
    connector_id               TEXT        NOT NULL,
    user_id                    TEXT        NOT NULL,
    -- access_token_blob holds the pkg/kms/envelope-encoded
    -- ciphertext of the OAuth access token. Never plaintext.
    access_token_blob          BYTEA       NOT NULL,
    access_token_key_version   INTEGER     NOT NULL DEFAULT 0,
    -- access_token_hash carries SHA-256(access_token) for the §13.3
    -- revocation-lookup index. The proxy reads it to find the
    -- credential corresponding to a presented bearer without
    -- decrypting every stored token.
    access_token_hash          BYTEA       NOT NULL,
    -- refresh_token_blob is the envelope-encoded refresh token.
    -- NULL when the provider issued none.
    refresh_token_blob         BYTEA,
    refresh_token_key_version  INTEGER,
    token_type                 TEXT        NOT NULL DEFAULT 'Bearer',
    -- scopes is the granted scope set, serialised as a JSON array.
    scopes                     JSONB       NOT NULL DEFAULT '[]'::jsonb,
    expires_at                 TIMESTAMPTZ,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- rotated_at records the last refresh-token exchange; NULL until
    -- the access token has been rotated at least once.
    rotated_at                 TIMESTAMPTZ,
    revoked_at                 TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, connector_id, user_id)
);

-- Revocation-lookup index keyed by the SHA-256(access_token) hash so
-- the §13.3 revocation hot path resolves a bearer to its row without
-- a table scan.
CREATE INDEX idx_connector_credentials_token_hash
    ON connector_credentials (tenant_id, access_token_hash);

-- List-by-connector index for the §9.3 admin path.
CREATE INDEX idx_connector_credentials_connector
    ON connector_credentials (tenant_id, connector_id, user_id);

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON connector_credentials
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

ALTER TABLE connector_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE connector_credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY lenny_tenant_isolation ON connector_credentials
    USING (
        tenant_id = current_setting('app.current_tenant', true)
        OR current_setting('app.current_tenant', false) = '__all__'
    );

GRANT SELECT, INSERT, UPDATE, DELETE ON connector_credentials TO lenny_app;
