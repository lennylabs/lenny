-- Reverses 0021_runtime_min_platform_version.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS min_platform_version;
