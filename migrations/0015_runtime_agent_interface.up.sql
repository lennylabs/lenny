-- The §5.1 agentInterface descriptor on a runtime.
--
-- agent_interface carries the optional §5.1 agentInterface block a
-- type:agent runtime declares for runtime discovery, A2A agent-card
-- auto-generation, and adapter manifest summaries. SQL NULL means the
-- runtime carries no agentInterface: every type:mcp runtime, and every
-- type:agent runtime that omits the block.

ALTER TABLE runtime_definitions
    ADD COLUMN agent_interface JSONB;
