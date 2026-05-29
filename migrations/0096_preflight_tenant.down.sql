-- Reverses migration 0096. Removes the reserved __preflight__ tenant.
-- Any agent_memory probe rows under it are removed first to satisfy the
-- foreign key; in steady state the erasure preflight self-cleans and
-- leaves no rows behind. Migrations run as the superuser, which bypasses
-- the agent_memory FORCE row-level-security policy.
DELETE FROM agent_memory WHERE tenant_id = '__preflight__';
DELETE FROM tenants WHERE id = '__preflight__';
