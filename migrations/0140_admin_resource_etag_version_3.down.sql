ALTER TABLE credential_pools
    DROP COLUMN IF EXISTS version;

ALTER TABLE connectors
    DROP COLUMN IF EXISTS version;
