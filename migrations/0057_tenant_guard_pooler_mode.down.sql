-- Reverses 0057_tenant_guard_pooler_mode.
--
-- Restores the migration 0047 form: accept __all__ unconditionally
-- (no lenny.allow_all_sentinel opt-in GUC check).

CREATE OR REPLACE FUNCTION lenny_tenant_guard() RETURNS trigger AS $$
DECLARE
    ctx        TEXT := current_setting('app.current_tenant', true);
    row_tenant TEXT;
BEGIN
    IF ctx IS NULL OR ctx = '' OR ctx = '__unset__' THEN
        RAISE EXCEPTION
            'lenny_tenant_guard: app.current_tenant is not set'
            USING ERRCODE = 'insufficient_privilege';
    END IF;

    IF ctx = '__all__' THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;

    IF ctx !~ '^[A-Za-z0-9_-]{1,128}$' THEN
        RAISE EXCEPTION
            'lenny_tenant_guard: app.current_tenant (%) is not a valid tenant id', ctx
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

-- Restore the migration 0051 RLS policy form: __all__ bypass with
-- no lenny.allow_all_sentinel opt-in check.
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
        'erasure_jobs', 'connector_credentials',
        'delegation_policies', 'connectors',
        'session_dlq_archive'
    ] LOOP
        IF EXISTS (
            SELECT 1 FROM pg_policies
            WHERE schemaname = current_schema()
              AND tablename = t
              AND policyname = 'lenny_tenant_isolation'
        ) THEN
            EXECUTE format('DROP POLICY IF EXISTS lenny_tenant_isolation ON %I', t);
            EXECUTE format(
                'CREATE POLICY lenny_tenant_isolation ON %I '
                || 'USING ('
                || '  tenant_id = current_setting(''app.current_tenant'', false) '
                || '  OR current_setting(''app.current_tenant'', false) = ''__all__'''
                || ')', t);
        END IF;
    END LOOP;
END $$;
