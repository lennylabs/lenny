-- Down migration for 0067: drop the session_checkpoints catalog.

DROP POLICY IF EXISTS lenny_tenant_isolation ON session_checkpoints;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON session_checkpoints;
DROP INDEX IF EXISTS idx_session_checkpoints_deleted_at;
DROP INDEX IF EXISTS idx_session_checkpoints_session_age;
DROP TABLE IF EXISTS session_checkpoints;
