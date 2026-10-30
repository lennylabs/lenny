-- Reverse 0108: drop the §12.6 PostgresPodRegistry watch trigger and
-- its notify function.

DROP TRIGGER IF EXISTS agent_pod_state_notify_trigger ON agent_pod_state;
DROP FUNCTION IF EXISTS agent_pod_state_notify();
