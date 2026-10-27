-- §12.3 (Write durability categories during failover) — the per-write
-- identifier a session state write stores in its row.
--
-- The session store's Update writes a fresh UUID into last_write_id on
-- every call. When the commit acknowledgement of that write is lost, the
-- gateway reads the row back from the primary: the row carries the write's
-- identifier only when that write committed and no later write replaced it.
-- updated_at cannot serve, because two replicas whose clocks lag the stored
-- value both stamp the stored value plus 1µs. The column is NULL until the
-- row's first Update, and it is not part of the session record the store
-- returns.
ALTER TABLE sessions
    ADD COLUMN last_write_id UUID;
