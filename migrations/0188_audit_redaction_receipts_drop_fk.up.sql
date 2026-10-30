-- spec: §12.8 (Compliance Interfaces: RedactionReceipt schema), §16.4
-- (Logging)
--
-- Drops the foreign key from audit_redaction_receipts.audit_event_id onto
-- audit_log.id that migration 0160 created with an inline REFERENCES.
--
-- A RedactionReceipt outlives the audit_log row it names: the §16.4
-- retention sweep, a partition force-drop, and tenant-deletion Phase 4
-- each delete audit rows and never delete receipts. With the key in
-- place, deleting a redacted dead-lettered row fails with SQLSTATE 23503.
-- ON DELETE SET NULL is not an option, because the receipt signature
-- covers audit_event_id and no role holds UPDATE on the table. Verifiers
-- locate a receipt by (tenant_id, sequence_number), so the column keeps
-- its value and its index without a referential constraint.
--
-- idx_audit_redaction_receipts_event and every grant stay as migration
-- 0160 created them.
ALTER TABLE audit_redaction_receipts
    DROP CONSTRAINT IF EXISTS audit_redaction_receipts_audit_event_id_fkey;
