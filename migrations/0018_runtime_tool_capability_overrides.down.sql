-- Reverses 0018_runtime_tool_capability_overrides.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS tool_capability_overrides;
