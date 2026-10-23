-- Reverses 0183_sessions_podless_suspended_index.
-- spec: §7.2 (Message delivery routing — seven paths, path 6).
DROP INDEX IF EXISTS idx_sessions_podless_suspended;
