-- Reverses 0010_session_legal_hold.
ALTER TABLE sessions
    DROP COLUMN IF EXISTS legal_hold;
