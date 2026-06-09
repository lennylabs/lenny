-- §12.8 step-14 OCSF dead-letter PII redaction grant. The erasure job
-- rewrites the `payload` of a `dead_lettered` audit_log row in place
-- (scrubbing the raw canonical PII while preserving the row for
-- pipeline-failure forensics) and recomputes `payload_canonical_json`
-- from the redacted payload. The lenny_audit_immutability trigger admits
-- that UPDATE only inside `SET LOCAL lenny.erasure_mode = 'true'`; this
-- grant gives the lenny_erasure role the column-level UPDATE privilege the
-- redaction needs. lenny_erasure already holds DELETE/INSERT/SELECT on
-- audit_log (migration 0002); it gains UPDATE on exactly the two columns
-- the redaction rewrites and nothing else, so the hash-input columns
-- (id, tenant_id, sequence_number, prev_hash, event_type,
-- event_schema_version, created_at) stay outside any role's UPDATE reach.
--
-- This is a COLUMN-scoped grant: it does not appear in
-- information_schema.role_table_grants as a table-level UPDATE, so the
-- §11.7 item-1 startup grant verification (which rejects table-level
-- UPDATE/DELETE on audit_log for lenny_app) is unaffected.
--
-- spec: §12.8 lines 810-827 (in-place dead-letter redaction); §11.7
-- item 3 (hash-input columns immutable outside the erasure path).
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_erasure') THEN
        GRANT UPDATE (payload, payload_canonical_json) ON audit_log TO lenny_erasure;
    END IF;
END $$;
