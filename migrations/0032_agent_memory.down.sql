-- Reverses 0032_agent_memory. Dropping the table cascades its
-- agent_memory_owner_created_idx index, lenny_tenant_guard trigger,
-- RLS policy, and lenny_app grants.
DROP TABLE IF EXISTS agent_memory;
