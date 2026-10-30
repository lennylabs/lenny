-- Reverses 0121_ops_remediation_locks.up.sql. Indexes and the unique
-- constraint drop with their tables.
DROP TABLE IF EXISTS ops_lock_conflicts;
DROP TABLE IF EXISTS ops_lock_epoch;
DROP TABLE IF EXISTS ops_remediation_locks;
