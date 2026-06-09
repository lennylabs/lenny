-- §12.8 lines 812-827 — the RedactionReceipt store. One append-only row
-- per audit_log row that the §12.8 step-14 OCSF dead-letter PII redaction
-- rewrites in place under GDPR Article 17. The receipt is the sole
-- provenance token that distinguishes an authorized GDPR redaction from a
-- tamper that happens to clear `payload`: a chain verifier that finds a
-- `redacted_gdpr` row without a signature-verifying receipt for the same
-- (tenant_id, sequence_number) MUST raise `broken` and fire
-- AuditRedactionReceiptMissing (§16.5). The §11.7 periodic background
-- integrity check reads this table on each cycle and increments
-- lenny_audit_redaction_receipt_missing_total per tenant for any orphaned
-- redaction-marked row. F-11.7.15.
--
-- The table holds no personal data (hashes and identifiers only, by
-- construction), so it is exempt from DeleteByUser / DeleteByTenant under
-- the same rationale as gdpr.* receipts (§12.8 line 827).
CREATE TABLE IF NOT EXISTS audit_redaction_receipts (
    receipt_id           UUID        PRIMARY KEY,
    -- §12.8 line 815 — FK to audit_log.id (the redacted row). audit_log
    -- carries a single-column UNIQUE (id) precisely so this FK is legal.
    audit_event_id       UUID        NOT NULL
        REFERENCES audit_log (id),
    tenant_id            TEXT        NOT NULL,
    sequence_number      BIGINT      NOT NULL,
    -- 32-byte SHA-256 of the canonical tuple before (original) and after
    -- (new) the in-place redaction; the verifier matches these against the
    -- observed chain rewrite boundary.
    original_hash        BYTEA       NOT NULL,
    new_hash             BYTEA       NOT NULL,
    erasure_job_id       UUID        NOT NULL,
    legal_basis          TEXT        NOT NULL,
    redactor_identity    TEXT        NOT NULL,
    "timestamp"          TIMESTAMPTZ NOT NULL,
    -- detached signature over the JCS-serialized receipt tuple, produced by
    -- the KMS-held audit signing key; signature_kms_key_id is recorded
    -- inline so a later key rotation leaves prior receipts verifiable.
    signature            BYTEA,
    signature_kms_key_id TEXT,
    -- One receipt per redacted row; verifiers locate the receipt by
    -- (tenant_id, sequence_number) position rather than by UUID.
    CONSTRAINT audit_redaction_receipts_position_unique
        UNIQUE (tenant_id, sequence_number),
    CONSTRAINT audit_redaction_receipts_legal_basis_check
        CHECK (legal_basis IN
            ('gdpr_art17',
             'gdpr_art17_with_art17_3_exception',
             'operator_acknowledged_override'))
);

CREATE INDEX idx_audit_redaction_receipts_event
    ON audit_redaction_receipts (audit_event_id);

-- §12.8 line 827 grant separation: lenny_erasure INSERTs receipts as it
-- redacts dead-lettered rows; lenny_app reads them for chain verification.
-- No role holds UPDATE or DELETE — the table is append-only like the
-- ledgers it protects.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_erasure') THEN
        GRANT INSERT ON audit_redaction_receipts TO lenny_erasure;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_app') THEN
        GRANT SELECT ON audit_redaction_receipts TO lenny_app;
    END IF;
END $$;
