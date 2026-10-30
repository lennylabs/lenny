-- Reverses 0122_ops_escalations.up.sql. The two indexes drop with the table.
DROP INDEX IF EXISTS ops_escalations_created_at;
DROP INDEX IF EXISTS ops_escalations_status_severity;
DROP TABLE IF EXISTS ops_escalations;
