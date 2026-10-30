-- Reverses 0039_credential_secret_envelope.
--
-- Drops the §4.9.1 key_version column and converts the secret column
-- back to its pre-0039 TEXT plaintext type. The TYPE conversion maps
-- the BYTEA bytes back to TEXT; because no deployment carries
-- credentials rows, no ciphertext is decrypted by this down migration.

ALTER TABLE credentials
    DROP COLUMN IF EXISTS secret_key_version;

ALTER TABLE credentials
    ALTER COLUMN secret DROP DEFAULT,
    ALTER COLUMN secret TYPE TEXT USING convert_from(secret, 'UTF8'),
    ALTER COLUMN secret SET DEFAULT '';
