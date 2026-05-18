-- §4 / §12.9 KMS envelope encryption for the end-user credential
-- registry's secret material.
--
-- Before this migration the credentials.secret column held the raw
-- secret material as plaintext TEXT (see migration 0036, which noted
-- "KMS-envelope encryption is a later phase"). §4.9 / §12.9 classify
-- user-supplied API keys as T4 Restricted: they must be stored under
-- AES-256-GCM envelope encryption, with a per-record data-encryption-
-- key (DEK) wrapped by a KMS key-encryption-key (KEK), and no
-- plaintext DEK persisted.
--
-- This migration converts the column to ciphertext storage:
--
--   secret             BYTEA — the envelope-encoded ciphertext blob
--                      produced by pkg/kms/envelope.Encode: the wrapped
--                      DEK, the AES-256-GCM nonce, and the record
--                      ciphertext. The plaintext secret never appears.
--   secret_key_version INTEGER — the §4.9.1 key_version: the KEK
--                      version that wrapped this row's DEK. The Token
--                      Service can decrypt with any known KEK version,
--                      so the §4.9.1 rotation procedure re-wraps rows
--                      version by version. 0 marks a row with no
--                      stored secret (the column default).
--
-- The column type changes from TEXT to BYTEA: ciphertext is binary.
-- There are no credentials rows in any deployment (the table was
-- created by migration 0036 in the same unreleased line), so the
-- conversion does not need to preserve or re-encrypt existing data;
-- the USING clause maps any present empty-string default to an empty
-- byte string.

ALTER TABLE credentials
    ALTER COLUMN secret DROP DEFAULT,
    ALTER COLUMN secret TYPE BYTEA USING convert_to(secret, 'UTF8'),
    ALTER COLUMN secret SET DEFAULT '\x';

ALTER TABLE credentials
    ADD COLUMN secret_key_version INTEGER NOT NULL DEFAULT 0;
