-- §11.2 per-tenant concurrent-session quota index.
--
-- The session-creation path enforces the §11.2 per-tenant
-- concurrent-session quota by counting the tenant's live (non-terminal)
-- sessions and rejecting the create once the count reaches
-- MaxConcurrentSessions. SessionStore.CountActiveSessions(tenant_id)
-- backs that check with a COUNT query so the gateway no longer
-- materializes every historical session row in Go on each create.
--
-- This partial index keeps the count cheap on a tenant with a large
-- session history: it is keyed on tenant_id and restricted to the
-- non-terminal states, so it indexes only the rows the count can
-- include. Terminal sessions are excluded. The four terminal states
-- match session.TerminalStates().
--
-- spec: spec/11_policy-and-controls.md §11.2 (per-tenant
-- concurrent-session quota with hard rejection).
CREATE INDEX idx_sessions_active_by_tenant
    ON sessions (tenant_id)
    WHERE state NOT IN ('completed', 'failed', 'cancelled', 'expired');
