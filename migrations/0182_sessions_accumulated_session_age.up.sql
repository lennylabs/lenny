-- §6.2 — persist a session's accumulated active age.
--
-- maxSessionAge measures active time: elapsed time counts toward the cap only
-- while the session is in starting, running, or input_required, and is paused
-- in suspended and in the recovery states. The gateway persists
-- accumulated_session_age_seconds and evaluates the cap as this value plus the
-- time elapsed since the row last entered a counting state. The session store
-- accrues the column on every row write, from the strictly advancing
-- updated_at, so the column holds the active time up to the row's last write.
--
-- Existing rows start at 0. Their active time before this migration is not
-- reconstructible from the row, so the cap restarts for them; the alternative
-- of backfilling wall-clock age would count paused time the cap excludes.
--
-- The column is additive and has a default, so a still-running old binary
-- that does not name it in its insert or update keeps working through the
-- rolling window.
--
-- spec: §6.2 (maxSessionAge timer behavior across states).

ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS accumulated_session_age_seconds double precision NOT NULL DEFAULT 0
        CHECK (accumulated_session_age_seconds >= 0);
