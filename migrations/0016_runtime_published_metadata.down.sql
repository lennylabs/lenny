-- Reverses 0016_runtime_published_metadata.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS published_metadata;
