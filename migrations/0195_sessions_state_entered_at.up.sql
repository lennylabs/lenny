-- §6.2 (Pod State Machine) — persist the instant a session entered its
-- current state.
--
-- state_entered_at anchors every per-state deadline that §6.2 measures from
-- the session's entry to a state. updated_at cannot anchor those deadlines,
-- because every write rewrites it. The session store sets state_entered_at to
-- created_at when it creates the row, sets it to the write's updated_at on
-- every write that changes the state, and leaves it unchanged otherwise.
--
-- A row that exists when this migration runs takes the migration instant, so
-- its per-state window restarts once, as migration 0182 restarts the active
-- age.
--
-- The trigger stamps a state change written by a binary that does not name
-- the column, which is an older replica during the rolling window. The
-- store's own state-changing write always carries a new value, so the
-- trigger leaves that write as it is.
--
-- input_wait_id names the live lenny/request_input wait a row in
-- input_required is waiting on. It is NULL on every other row.
--
-- spec: §6.2 (Pod State Machine), §7.2 (Interactive Session Model).

ALTER TABLE sessions ADD COLUMN state_entered_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE sessions ADD COLUMN input_wait_id text;

CREATE FUNCTION sessions_stamp_state_entry() RETURNS trigger AS $$
BEGIN
    IF NEW.state IS DISTINCT FROM OLD.state
       AND NEW.state_entered_at IS NOT DISTINCT FROM OLD.state_entered_at THEN
        NEW.state_entered_at := NEW.updated_at;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER sessions_stamp_state_entry
    BEFORE UPDATE ON sessions
    FOR EACH ROW EXECUTE FUNCTION sessions_stamp_state_entry();
