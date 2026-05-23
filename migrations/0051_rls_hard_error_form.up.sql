-- §4.2 line 163: every tenant-scoped table's RLS policy must use
-- current_setting('app.current_tenant', false) so an unset GUC raises
-- rather than silently falling through.
--
-- Migration 0047 set the __all__ OR-clause but kept the lenient
-- `true` form on the per-tenant predicate. A read that reaches the
-- policy with no app.current_tenant SET would return an empty string
-- from current_setting(..., true), which the policy then compares to
-- the row's tenant_id — silently filtering all rows out instead of
-- failing closed. The `false` form raises a configuration_invalid
-- error when the GUC is unset, which is the spec-mandated behaviour.
--
-- The __all__ OR-clause continues to use the `false` form: that read
-- is already a deliberate platform-admin code path, and the trigger
-- guard rejects __unset__/empty before any policy evaluates.
--
-- session_eviction_state (migration 0045) already uses the `false`
-- form under its dedicated policy name and is intentionally
-- untouched. connector_credentials (migration 0048) is rewritten here
-- in line with the other tenant-scoped tables.

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
            || '  tenant_id = current_setting(''app.current_tenant'', false) '
            || '  OR current_setting(''app.current_tenant'', false) = ''__all__'''
            || ')', t);
    END LOOP;
END $$;
