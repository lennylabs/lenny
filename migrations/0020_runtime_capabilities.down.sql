-- Reverses 0020_runtime_capabilities.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS capabilities;
