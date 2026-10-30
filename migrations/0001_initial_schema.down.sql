-- Reverses 0001_initial_schema. Tables are dropped in FK-dependency
-- order: session_messages references sessions and tenants; sessions,
-- runtime_definitions, audit_log, billing_events, and issued_tokens
-- reference tenants; agent_pod_state references nothing.

DROP TABLE IF EXISTS session_messages;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS runtime_definitions;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS billing_events;
DROP TABLE IF EXISTS issued_tokens;
DROP TABLE IF EXISTS agent_pod_state;
DROP TABLE IF EXISTS tenants;
