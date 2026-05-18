-- §11.7 / §12.3.7 audit pipeline state-machine columns.
--
-- The §11.7 OCSF translator and the §12.3.7 EventBus retranscribe
-- worker advance per-row bookkeeping on audit_log: ocsf_translation_state
-- runs pending -> retry_pending -> succeeded | dead_lettered, and
-- eventbus_publish_state runs pending -> retry_pending -> published |
-- failed, with retry_count tracking attempts. §12.3.7 is explicit that
-- the retranscribe worker "updates only eventbus_publish_state and
-- retry_count, both of which are excluded from the payload_canonical_json
-- hash input" so the prev_hash chain is never re-hashed.
--
-- The original lenny_audit_immutability trigger (migration 0002)
-- rejects EVERY UPDATE, which makes the spec-mandated state machine
-- impossible. This migration narrows the trigger so it still protects
-- the audited content and the hash chain — every hash-input column and
-- the payload jsonb remain immutable — while permitting an UPDATE that
-- touches ONLY the three non-hash bookkeeping columns. The append-only
-- guarantee on the audit trail is preserved: the canonical tuple a
-- verifier hashes cannot be altered outside the erasure path.

-- Replace lenny_audit_immutability. The erasure-mode bypass clause is
-- retained verbatim (the gateway startup check in pkg/audit/integrity
-- inspects pg_proc.prosrc for it). A non-erasure UPDATE is now allowed
-- only when the hash-input columns and the payload jsonb are byte-for-
-- byte unchanged; any other UPDATE is still rejected.
CREATE OR REPLACE FUNCTION lenny_audit_immutability() RETURNS trigger AS $$
BEGIN
    IF current_setting('lenny.erasure_mode', true) = 'true' THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION
            'lenny_audit_immutability: audit_log is append-only; DELETE rejected'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    -- TG_OP = 'UPDATE'. Permit the §11.7 / §12.3.7 state-machine write
    -- only: the hash-input columns and the audited payload must be
    -- unchanged. ocsf_translation_state, eventbus_publish_state, and
    -- retry_count are the non-hash bookkeeping columns the translator
    -- and the retranscribe worker advance.
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.sequence_number IS DISTINCT FROM OLD.sequence_number
        OR NEW.prev_hash IS DISTINCT FROM OLD.prev_hash
        OR NEW.event_type IS DISTINCT FROM OLD.event_type
        OR NEW.event_schema_version IS DISTINCT FROM OLD.event_schema_version
        OR NEW.payload IS DISTINCT FROM OLD.payload
        OR NEW.payload_canonical_json IS DISTINCT FROM OLD.payload_canonical_json
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION
            'lenny_audit_immutability: audit_log is append-only; UPDATE may touch only ocsf_translation_state, eventbus_publish_state, retry_count'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- lenny_app may now UPDATE the three audit-pipeline bookkeeping
-- columns. This is a COLUMN-scoped grant: it does NOT appear in
-- information_schema.role_table_grants as a table-level UPDATE, so the
-- §11.7 item 1 startup grant verification (which checks for table-level
-- UPDATE/DELETE on audit_log) still passes. The gateway can advance the
-- OCSF and EventBus state machines; it still cannot UPDATE any
-- hash-input column or the payload.
GRANT UPDATE (ocsf_translation_state, eventbus_publish_state, retry_count)
    ON audit_log TO lenny_app;
