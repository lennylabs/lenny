-- §11.3 (Session expiry warning) — record on the session row the deadline the
-- session_expiring_soon warning was sent for.
--
-- Every gateway replica runs the session watchdog's sweeps over every row
-- (§10.1.1 Primary:), so a per-process record of which sessions were warned
-- sends one warning per replica. The expiry-warning sweep instead claims the
-- warning with a locked-row write that sets expiry_warned_cap_seconds to the
-- session's effective maxSessionAge cap in whole seconds, after re-checking on
-- the locked row that the session is non-terminal, its age timer runs, and its
-- remaining active time is inside the warning band. Only the replica whose
-- write applied sends the warning, so it goes out once per session per
-- deadline. A cap that changes after the warning makes the row due again for
-- the new deadline.
--
-- The value is zero until the first warning. A row that exists when this
-- migration runs takes zero, so a session already inside the band when the
-- migration runs is warned once more by the new binary.
ALTER TABLE sessions
    ADD COLUMN expiry_warned_cap_seconds bigint NOT NULL DEFAULT 0
        CONSTRAINT sessions_expiry_warned_cap_seconds_nonnegative
        CHECK (expiry_warned_cap_seconds >= 0);
