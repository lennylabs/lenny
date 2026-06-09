-- §6.2 lines 273-300 / §11.3 line 199 — `maxIdleTime` enforcement.
-- "Idle" is no qualifying agent activity since `last_agent_activity_at`.
-- The §11.3 watchdog reads this column to expire a `running` session that
-- has been idle longer than its effective `maxIdleTimeSeconds`. It cannot
-- reuse `updated_at`, which advances on internal state writes (status
-- changes, checkpoint stamps) that are not agent activity. The column is
-- nullable: a NULL value (legacy rows, sessions that never recorded a
-- qualifying event) makes the watchdog fall back to `updated_at` (the
-- running-entry time), so the idle clock is always anchored to a real
-- instant. F-11.3.7.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS last_agent_activity_at TIMESTAMPTZ;
