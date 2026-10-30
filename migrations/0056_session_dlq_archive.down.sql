-- Reverses 0056_session_dlq_archive.

DROP POLICY IF EXISTS lenny_tenant_isolation ON session_dlq_archive;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON session_dlq_archive;
DROP INDEX IF EXISTS idx_session_dlq_archive_tenant_archived;
DROP TABLE IF EXISTS session_dlq_archive;
