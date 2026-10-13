-- Reverse 0182: drop the accumulated active-age column and its CHECK.
-- spec: §6.2 (maxSessionAge timer behavior across states).
ALTER TABLE sessions DROP COLUMN IF EXISTS accumulated_session_age_seconds;
