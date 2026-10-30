-- Reverses 0014_session_environment.
ALTER TABLE sessions
    DROP COLUMN IF EXISTS environment;
