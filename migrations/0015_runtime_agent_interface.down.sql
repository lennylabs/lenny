-- Reverses 0015_runtime_agent_interface.
ALTER TABLE runtime_definitions
    DROP COLUMN IF EXISTS agent_interface;
