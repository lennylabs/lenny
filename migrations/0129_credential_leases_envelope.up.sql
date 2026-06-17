-- §12.9 T4-Restricted envelope encryption for the §4.9 credential-lease
-- store.
--
-- The §12.9 default-mapping table classifies a credential lease as
-- T4 — Restricted. The persisted lease body carries the §4.9 proxy-mode
-- bearer lease token, the capability a runtime presents to the LLM
-- reverse proxy. Migration 0038 stored the body as plaintext JSONB and
-- the bearer token as a plaintext indexed column, so any lenny_app
-- SELECT exposed the capability in cleartext. This migration brings the
-- lease store to the AES-256-GCM envelope-encryption posture migration
-- 0039 gave the T4 credential-secret registry: a per-record DEK wrapped
-- by a KMS KEK, with no plaintext DEK and no plaintext capability
-- persisted.
--
-- Changes:
--
--   lease              JSONB -> BYTEA. The body becomes the
--                      pkg/kms/envelope-encoded ciphertext blob (the
--                      wrapped DEK, the GCM nonce, and the record
--                      ciphertext). The plaintext lease never reaches
--                      Postgres.
--   lease_key_version  INTEGER — the §4.9.1 KEK version that wrapped the
--                      row's DEK, the platform-scoped
--                      "platform:credential-leases" KEK. 0 marks a row
--                      with no stored body (the column default).
--   lease_token        dropped. The bearer token is no longer stored.
--   lease_token_hash   TEXT — the SHA-256 hex digest of a proxy-mode
--                      lease's bearer token. GetByToken resolves a
--                      presented token by hashing it and matching this
--                      column, so the token itself is never persisted.
--                      A direct-mode lease carries a NULL hash.
--   session_id,        the non-secret routing identifiers the §7.1
--   cred_source,       teardown and §11.4 full_revoke / §4.9 emergency
--   pool_id,           revocation paths query leases by. They were read
--   credential_id,     from the JSONB body before; with the body
--   cred_tenant_id,    encrypted they move to dedicated plaintext
--   credential_ref     columns so those lookups stay indexed without
--                      decrypting every row. Source plus
--                      (pool_id, credential_id) for a pool-backed lease,
--                      or (cred_tenant_id, credential_ref) for a
--                      user-backed lease, mirror
--                      credential.Lease.CredentialKey.
--
-- This conversion does not preserve or re-encrypt existing data: the plaintext
-- JSONB lease body is reinterpreted as a ciphertext blob and the plaintext
-- bearer token is dropped, with no per-row backfill.
--
-- §10.5 Phase 3 column drop (spec §10.5 line 417). The DROP COLUMN lease_token
-- is irreversible and the lease-body type change reinterprets stored bytes
-- without decrypting them, so the up-file is fronted by a PL/pgSQL DO $$
-- preflight gate that counts un-migrated rows and RAISE EXCEPTIONs when any
-- remain. Any existing credential_leases row is un-migrated data: its plaintext
-- body and bearer token are not converted forward, so applying the reshape
-- would corrupt the body and lose the token. The gate fails closed on any such
-- row. The whole up-file runs in one transaction, so a RAISE EXCEPTION rolls
-- back the entire migration. The drop is idempotent (DROP COLUMN IF EXISTS) so a
-- re-run after the gate passes is a no-op.
-- gate-index: credential_leases_pkey
DO $$
DECLARE remaining bigint;
BEGIN
    SELECT COUNT(*) INTO remaining FROM credential_leases;
    IF remaining > 0 THEN
        RAISE EXCEPTION 'Phase 3 gate failed: % un-migrated rows remain in credential_leases (the §12.9 envelope conversion backfills no plaintext lease body or bearer token). Resolve data migration before retrying.', remaining;
    END IF;
END $$;

ALTER TABLE credential_leases
    ALTER COLUMN lease TYPE BYTEA USING convert_to(lease::text, 'UTF8'),
    ALTER COLUMN lease SET DEFAULT '\x',
    ADD COLUMN lease_key_version INTEGER NOT NULL DEFAULT 0;

-- Dropping lease_token drops idx_credential_leases_token from migration
-- 0038 with it. lease_token_hash replaces it as the GetByToken lookup
-- key so the plaintext bearer token is not stored.
ALTER TABLE credential_leases
    DROP COLUMN IF EXISTS lease_token,
    ADD COLUMN lease_token_hash TEXT,
    ADD COLUMN session_id     TEXT NOT NULL DEFAULT '',
    ADD COLUMN cred_source    TEXT NOT NULL DEFAULT '',
    ADD COLUMN pool_id        TEXT NOT NULL DEFAULT '',
    ADD COLUMN credential_id  TEXT NOT NULL DEFAULT '',
    ADD COLUMN cred_tenant_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN credential_ref TEXT NOT NULL DEFAULT '';

-- A proxy-mode lease token hash is unique across the store; direct-mode
-- rows carry a NULL hash and are excluded from the constraint.
CREATE UNIQUE INDEX idx_credential_leases_token_hash
    ON credential_leases (lease_token_hash)
    WHERE lease_token_hash IS NOT NULL;

-- LeasesBySession filters on session_id (the §7.1 teardown / §11.4
-- full_revoke path).
CREATE INDEX idx_credential_leases_session
    ON credential_leases (session_id);

-- LeasesByCredential filters on the source-aware credential key (the
-- §4.9 emergency credential-revocation path).
CREATE INDEX idx_credential_leases_pool_cred
    ON credential_leases (cred_source, pool_id, credential_id);
CREATE INDEX idx_credential_leases_user_cred
    ON credential_leases (cred_source, cred_tenant_id, credential_ref);
