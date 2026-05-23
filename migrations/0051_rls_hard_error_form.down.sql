-- Revert the §4.2 line 163 hard-error rewrite: restore the
-- migration-0047 lenient `true` form on the per-tenant predicate.

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
        'erasure_jobs', 'connector_credentials'
    ] LOOP
        EXECUTE format('DROP POLICY IF EXISTS lenny_tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY lenny_tenant_isolation ON %I '
            || 'USING ('
            || '  tenant_id = current_setting(''app.current_tenant'', true) '
            || '  OR current_setting(''app.current_tenant'', false) = ''__all__'''
            || ')', t);
    END LOOP;
END $$;
