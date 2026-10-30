-- Reverses 0129_credential_leases_envelope.
--
-- Drops the §12.9 envelope columns and the plaintext routing columns,
-- restores the plaintext lease_token column and its unique index, and
-- converts the lease body back to its pre-0129 JSONB plaintext type.
-- Because no deployment carries credential_leases rows, no ciphertext
-- is decrypted and no JSON is re-parsed by this down migration.

DROP INDEX IF EXISTS idx_credential_leases_user_cred;
DROP INDEX IF EXISTS idx_credential_leases_pool_cred;
DROP INDEX IF EXISTS idx_credential_leases_session;
DROP INDEX IF EXISTS idx_credential_leases_token_hash;

ALTER TABLE credential_leases
    DROP COLUMN IF EXISTS lease_token_hash,
    DROP COLUMN IF EXISTS lease_key_version,
    DROP COLUMN IF EXISTS session_id,
    DROP COLUMN IF EXISTS cred_source,
    DROP COLUMN IF EXISTS pool_id,
    DROP COLUMN IF EXISTS credential_id,
    DROP COLUMN IF EXISTS cred_tenant_id,
    DROP COLUMN IF EXISTS credential_ref,
    ADD COLUMN lease_token TEXT;

ALTER TABLE credential_leases
    ALTER COLUMN lease DROP DEFAULT,
    ALTER COLUMN lease TYPE JSONB USING convert_from(lease, 'UTF8')::jsonb;

CREATE UNIQUE INDEX idx_credential_leases_token
    ON credential_leases (lease_token)
    WHERE lease_token IS NOT NULL;
