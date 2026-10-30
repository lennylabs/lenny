ALTER TABLE sessions
    DROP COLUMN IF EXISTS env,
    DROP COLUMN IF EXISTS request_envelope;
