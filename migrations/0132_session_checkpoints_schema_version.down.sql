-- Reverses 0132_session_checkpoints_schema_version.

ALTER TABLE session_checkpoints
    DROP COLUMN IF EXISTS schema_version;
