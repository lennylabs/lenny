-- Reverse of 0065_session_eviction_state_tenant_guard.up.sql.

DROP TRIGGER IF EXISTS lenny_tenant_guard ON session_eviction_state;
