-- Reverses 0185_sessions_counted_by_tenant_index: drops the counted-session
-- index and restores the migration-0102 non-terminal index.
DROP INDEX IF EXISTS idx_sessions_counted_by_tenant;

CREATE INDEX idx_sessions_active_by_tenant
    ON sessions (tenant_id)
    WHERE state NOT IN ('completed', 'failed', 'cancelled', 'expired');
