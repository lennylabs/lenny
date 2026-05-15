-- Reverses 0002_rls_immutability_roles. Triggers are dropped before
-- their functions; grants are revoked via DROP OWNED BY before the
-- roles themselves are dropped.

DROP TRIGGER IF EXISTS lenny_tenant_guard ON sessions;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON session_messages;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON issued_tokens;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON audit_log;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON billing_events;
DROP TRIGGER IF EXISTS lenny_audit_immutability ON audit_log;
DROP TRIGGER IF EXISTS lenny_erasure_insert_guard ON audit_log;
DROP TRIGGER IF EXISTS lenny_billing_immutability ON billing_events;

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'sessions', 'session_messages',
        'issued_tokens', 'audit_log', 'billing_events'
    ] LOOP
        EXECUTE format('DROP POLICY IF EXISTS lenny_tenant_isolation ON %I', t);
        EXECUTE format('ALTER TABLE %I DISABLE ROW LEVEL SECURITY', t);
    END LOOP;
END $$;

DROP FUNCTION IF EXISTS lenny_tenant_guard();
DROP FUNCTION IF EXISTS lenny_audit_immutability();
DROP FUNCTION IF EXISTS lenny_billing_immutability();
DROP FUNCTION IF EXISTS lenny_erasure_insert_guard();

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_app') THEN
        EXECUTE 'DROP OWNED BY lenny_app';
        DROP ROLE lenny_app;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lenny_erasure') THEN
        EXECUTE 'DROP OWNED BY lenny_erasure';
        DROP ROLE lenny_erasure;
    END IF;
END $$;
