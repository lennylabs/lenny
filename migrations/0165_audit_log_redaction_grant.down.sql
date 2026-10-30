-- Reverse 0165: revoke the §12.8 step-14 redaction UPDATE grant.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_erasure') THEN
        REVOKE UPDATE (payload, payload_canonical_json) ON audit_log FROM lenny_erasure;
    END IF;
END $$;
