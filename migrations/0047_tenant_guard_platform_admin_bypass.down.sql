-- Revert the §4.2 / §12.3 platform-admin __all__ bypass.

CREATE OR REPLACE FUNCTION lenny_tenant_guard() RETURNS trigger AS $$
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

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'sessions', 'session_messages', 'issued_tokens',
        'audit_log', 'billing_events',
        'users', 'idempotency_keys', 'custom_roles', 'environments',
        'eval_results', 'experiment_definitions', 'interactions',
        'agent_memory', 'usage_events', 'credentials', 'credential_pools',
        'erasure_jobs'
    ] LOOP
        EXECUTE format('DROP POLICY IF EXISTS lenny_tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY lenny_tenant_isolation ON %I '
            || 'USING (tenant_id = current_setting(''app.current_tenant'', true))', t);
    END LOOP;
END $$;
