-- Reverses 0187_session_reservations: drops the session_reservations
-- table, and with it its index, tenant-guard trigger, isolation policy,
-- and lenny_app grant.
DROP TABLE IF EXISTS session_reservations;
