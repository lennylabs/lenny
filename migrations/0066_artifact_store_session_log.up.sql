-- §4.4 line 226 lists "Session logs and runtime stderr" among the
-- artifacts the Event/Checkpoint Store retains for session recovery
-- and observability. The session-log store
-- (pkg/gateway/sessionlogstore) writes runtime stderr to MinIO at
-- `/{tenant_id}/sessions/{session_id}/stderr.log` and records an
-- artifact_store row alongside so the bytes participate in the §12.5
-- GC catalog and the per-tenant storage quota.
--
-- The artifact_store CHECK constraint enumerates the closed set of
-- spec-defined artifact kinds; this migration extends it to include
-- 'session_log'. Subsequent code paths that write session-log
-- artifacts set `artifact_type = 'session_log'` on the row; the §12.5
-- GC sweep retires session-log rows on the same lifecycle as every
-- other artifact kind.
--
-- spec: §4.4 line 226.

ALTER TABLE artifact_store
    DROP CONSTRAINT IF EXISTS artifact_store_artifact_type_check;

ALTER TABLE artifact_store
    ADD CONSTRAINT artifact_store_artifact_type_check
    CHECK (artifact_type IN ('workspace', 'eviction_context', 'checkpoint', 'export', 'session_log'));
