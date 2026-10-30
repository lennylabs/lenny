-- Reverses 0006_tenant_concurrent_session_quota.
ALTER TABLE tenants DROP COLUMN max_concurrent_sessions;
