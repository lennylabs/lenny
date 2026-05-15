-- Tenant isolation, ledger immutability, and database role separation
-- (Phase 1.5, build-sequence §18.5).
--
--   * lenny_tenant_guard       — §12.3 transaction-mode pooler guard.
--   * lenny_audit_immutability — §11.7 item 2 append-only audit_log.
--   * lenny_billing_immutability — §11.7 item 2 append-only billing.
--   * lenny_app / lenny_erasure — §11.7 items 1 and 7 role separation.
--
-- Row-level security policies back the trigger guard: the trigger
-- rejects writes that lack app.current_tenant (it fires for every
-- role, including superusers); RLS filters reads for the non-superuser
-- lenny_app role.

-- --- Database roles ------------------------------------------------------
-- lenny_app is the gateway's role: full DML on the operational tables,
-- INSERT-only on the audit and billing ledgers. lenny_erasure is the
-- GDPR erasure job's role: the only role that may UPDATE billing PII
-- or DELETE audit rows.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_app') THEN
        CREATE ROLE lenny_app NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_erasure') THEN
        CREATE ROLE lenny_erasure NOLOGIN;
    END IF;
END $$;

GRANT USAGE ON SCHEMA public TO lenny_app, lenny_erasure;

-- --- lenny_tenant_guard -------------------------------------------------
-- Rejects any INSERT/UPDATE/DELETE on a tenant-scoped table when
-- app.current_tenant is unset or does not match the row's tenant_id.
-- This closes the cross-tenant leak that transaction-mode connection
-- poolers can otherwise open (§12.3).
CREATE FUNCTION lenny_tenant_guard() RETURNS trigger AS $$
DECLARE
    ctx        TEXT := current_setting('app.current_tenant', true);
    row_tenant TEXT;
BEGIN
    IF ctx IS NULL OR ctx = '' THEN
        RAISE EXCEPTION
            'lenny_tenant_guard: app.current_tenant is not set'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF TG_OP = 'DELETE' THEN
        row_tenant := OLD.tenant_id;
    ELSE
        row_tenant := NEW.tenant_id;
    END IF;
    IF row_tenant IS DISTINCT FROM ctx THEN
        RAISE EXCEPTION
            'lenny_tenant_guard: row tenant_id (%) does not match app.current_tenant (%)',
            row_tenant, ctx
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- --- Immutability trigger functions -------------------------------------
-- audit_log and billing_events are append-only. UPDATE and DELETE are
-- rejected unless the transaction has set lenny.erasure_mode = 'true'
-- (the GDPR erasure path, §11.7 item 7). The guard clause reading
-- lenny.erasure_mode is verified at gateway startup by inspecting the
-- function body, so it must remain present in both functions.
CREATE FUNCTION lenny_audit_immutability() RETURNS trigger AS $$
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

CREATE FUNCTION lenny_billing_immutability() RETURNS trigger AS $$
BEGIN
    IF current_setting('lenny.erasure_mode', true) = 'true' THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION
        'lenny_billing_immutability: billing_events is append-only; % rejected', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;

-- lenny_erasure may INSERT into audit_log only to write GDPR erasure
-- receipts (event_type LIKE 'gdpr.%'); §11.7 item 7.
CREATE FUNCTION lenny_erasure_insert_guard() RETURNS trigger AS $$
BEGIN
    IF current_user = 'lenny_erasure' AND NEW.event_type NOT LIKE 'gdpr.%' THEN
        RAISE EXCEPTION
            'lenny_erasure may only INSERT gdpr.* rows into audit_log'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- --- Attach triggers + RLS to the tenant-scoped tables ------------------
CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON sessions
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();
CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON session_messages
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();
CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON runtime_definitions
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();
CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON issued_tokens
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();
CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();
CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON billing_events
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();

CREATE TRIGGER lenny_audit_immutability
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION lenny_audit_immutability();
CREATE TRIGGER lenny_erasure_insert_guard
    BEFORE INSERT ON audit_log
    FOR EACH ROW EXECUTE FUNCTION lenny_erasure_insert_guard();
CREATE TRIGGER lenny_billing_immutability
    BEFORE UPDATE OR DELETE ON billing_events
    FOR EACH ROW EXECUTE FUNCTION lenny_billing_immutability();

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'sessions', 'session_messages', 'runtime_definitions',
        'issued_tokens', 'audit_log', 'billing_events'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format(
            'CREATE POLICY lenny_tenant_isolation ON %I '
            || 'USING (tenant_id = current_setting(''app.current_tenant'', true))', t);
    END LOOP;
END $$;

-- --- Grants -------------------------------------------------------------
-- lenny_app: full DML on operational tables; INSERT + SELECT only on
-- the audit and billing ledgers (no UPDATE, no DELETE — §11.7 item 1).
GRANT SELECT, INSERT, UPDATE, DELETE ON
    tenants, runtime_definitions, sessions, session_messages,
    issued_tokens, agent_pod_state
    TO lenny_app;
GRANT SELECT, INSERT ON audit_log, billing_events TO lenny_app;

-- lenny_erasure: UPDATE on the billing PII column only, DELETE on
-- audit_log, INSERT on audit_log (gdpr.* rows only, enforced by
-- lenny_erasure_insert_guard), and SELECT for erasure verification.
GRANT UPDATE (user_id) ON billing_events TO lenny_erasure;
GRANT DELETE, INSERT, SELECT ON audit_log TO lenny_erasure;
GRANT SELECT ON billing_events TO lenny_erasure;
