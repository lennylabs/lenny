ALTER TABLE environments
    DROP COLUMN IF EXISTS version;

ALTER TABLE users
    DROP COLUMN IF EXISTS version;
