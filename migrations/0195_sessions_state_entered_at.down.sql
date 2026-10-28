-- Reverse 0195: drop the state-entry trigger, its function, the
-- state_entered_at column, and the input_wait_id column, in that order.
-- spec: §6.2 (Pod State Machine), §7.2 (Interactive Session Model).
DROP TRIGGER IF EXISTS sessions_stamp_state_entry ON sessions;
DROP FUNCTION IF EXISTS sessions_stamp_state_entry();
ALTER TABLE sessions DROP COLUMN IF EXISTS state_entered_at;
ALTER TABLE sessions DROP COLUMN IF EXISTS input_wait_id;
