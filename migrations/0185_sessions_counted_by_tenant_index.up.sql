-- spec: §11.1 (Concurrent-session count)
--
-- Concurrent-session count index.
--
-- The concurrent-session admission caps (the §11.2 per-tenant quota and
-- the §11.1 per-user, per-runtime, and global limits) count a session
-- from its creation until it reaches a terminal state, except while it
-- is suspended. The four SessionStore count queries spell that rule as
-- state NOT IN ('completed', 'failed', 'cancelled', 'expired',
-- 'suspended'), which session.CountsTowardConcurrencyCaps defines in Go.
--
-- The migration-0102 index idx_sessions_active_by_tenant excluded only
-- the terminal states, so a count under it read a heap tuple for every
-- suspended row of the tenant. Suspended rows are bounded by no cap and
-- persist for up to gateway.maxSuspendedSessionSeconds. This index
-- replaces it with the count predicate verbatim and adds user_id, so the
-- tenant and per-user counts read it index-only, the per-runtime count
-- reads it by its tenant prefix, and the global count scans it.
DROP INDEX IF EXISTS idx_sessions_active_by_tenant;

CREATE INDEX idx_sessions_counted_by_tenant
    ON sessions (tenant_id, user_id)
    WHERE state NOT IN ('completed', 'failed', 'cancelled', 'expired', 'suspended');
