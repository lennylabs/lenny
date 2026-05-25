-- Reverses 0073_credentials_last_used_at.
ALTER TABLE credentials
    DROP COLUMN IF EXISTS last_used_at;
