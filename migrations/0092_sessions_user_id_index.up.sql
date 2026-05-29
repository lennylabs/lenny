-- spec: §11.4 line 256
-- Add a per-user composite index over sessions so the §11.4 full_revoke
-- step-1 SessionStore lookup ("Gateway looks up all active sessions for
-- the user") is O(user's sessions) instead of O(tenant's sessions). The
-- index is composite on (tenant_id, user_id) so the per-tenant tenancy
-- filter shares the b-tree prefix with the user filter.

CREATE INDEX IF NOT EXISTS idx_sessions_tenant_user
    ON sessions (tenant_id, user_id);
