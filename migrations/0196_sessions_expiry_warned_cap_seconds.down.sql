-- Reverse 0196: drop the expiry_warned_cap_seconds column and its CHECK.
-- spec: §11.3 (Session expiry warning).
ALTER TABLE sessions DROP COLUMN IF EXISTS expiry_warned_cap_seconds;
