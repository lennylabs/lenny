-- Drop the §16.4 partitioned EventStore tables. DROP TABLE on a
-- range-partitioned parent removes the parent, every dated partition the
-- partitionmaint maintainer created, and the DEFAULT partition.
DROP POLICY IF EXISTS lenny_tenant_isolation ON stream_cursors;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON stream_cursors;
DROP TABLE IF EXISTS stream_cursors;

DROP POLICY IF EXISTS lenny_tenant_isolation ON session_logs;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON session_logs;
DROP TABLE IF EXISTS session_logs;
