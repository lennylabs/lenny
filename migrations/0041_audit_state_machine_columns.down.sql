-- Reverses 0041_audit_state_machine_columns: restores the original
-- lenny_audit_immutability trigger function (every non-erasure UPDATE
-- rejected) and revokes the column-scoped UPDATE grant on the three
-- audit-pipeline bookkeeping columns.
REVOKE UPDATE (ocsf_translation_state, eventbus_publish_state, retry_count)
    ON audit_log FROM lenny_app;

CREATE OR REPLACE FUNCTION lenny_audit_immutability() RETURNS trigger AS $$
BEGIN
    IF current_setting('lenny.erasure_mode', true) = 'true' THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION
        'lenny_audit_immutability: audit_log is append-only; % rejected', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;
