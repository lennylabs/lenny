-- Reverses 0131_session_messages_schema_version.

ALTER TABLE session_messages
    DROP COLUMN IF EXISTS schema_version;
