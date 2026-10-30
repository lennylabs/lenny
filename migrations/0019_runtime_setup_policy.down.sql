-- Reverses 0019_runtime_setup_policy.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS setup_policy;
