-- Reverses 0188_audit_redaction_receipts_drop_fk: re-adds the foreign key
-- from audit_redaction_receipts.audit_event_id onto audit_log.id. The key
-- is added NOT VALID because receipts whose audit row was deleted while
-- the key was absent would fail validation; Postgres enforces the key on
-- new rows only.
ALTER TABLE audit_redaction_receipts
    ADD CONSTRAINT audit_redaction_receipts_audit_event_id_fkey
    FOREIGN KEY (audit_event_id) REFERENCES audit_log (id) NOT VALID;
