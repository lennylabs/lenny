-- Reverses 0050_session_record_fields.

ALTER TABLE sessions
    DROP COLUMN IF EXISTS cwd,
    DROP COLUMN IF EXISTS pod_assignment,
    DROP COLUMN IF EXISTS recovery_generation,
    DROP COLUMN IF EXISTS coordination_generation,
    DROP COLUMN IF EXISTS schema_version;
