-- Reverses 0023_runtime_base_runtime.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS base_runtime;
