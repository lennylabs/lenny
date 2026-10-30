-- Down migration for 0066: restore the prior CHECK constraint that
-- omits the 'session_log' artifact kind. Any session_log rows must be
-- removed before this migration runs.

DELETE FROM artifact_store WHERE artifact_type = 'session_log';

ALTER TABLE artifact_store
    DROP CONSTRAINT IF EXISTS artifact_store_artifact_type_check;

ALTER TABLE artifact_store
    ADD CONSTRAINT artifact_store_artifact_type_check
    CHECK (artifact_type IN ('workspace', 'eviction_context', 'checkpoint', 'export'));
