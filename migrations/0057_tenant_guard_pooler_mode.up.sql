-- §4.2 line 165 pooler-mode guard for the __all__ cross-tenant
-- sentinel.
--
-- spec: §4.2 line 165 — "the __all__ sentinel is rejected by the
-- lenny_tenant_guard trigger when LENNY_POOLER_MODE = external and
-- the trigger is configured to allow only concrete tenant IDs (i.e.,
-- __all__ must be explicitly allowlisted in the trigger alongside
-- concrete IDs)."
--
-- The trigger has no direct access to the gateway's
-- LENNY_POOLER_MODE env var; instead it gates the __all__ sentinel
-- on a session GUC `lenny.allow_all_sentinel`. The gateway sets the
-- GUC to 'true' via SET LOCAL on every code path that wraps a call
-- in pgtenant.InAllTenants (which is the only platform-admin
-- cross-tenant code path). In transactional pooler mode this is
-- functionally identical to migration 0047's behaviour: the
-- platform-admin path always succeeds because the gateway opted in.
-- In `external` pooler mode an out-of-process leak that bypasses
-- the gateway's helper carries no opt-in GUC; the trigger then
-- rejects the __all__ value, satisfying the spec rule that the
-- sentinel must be "explicitly allowlisted in the trigger alongside
-- concrete IDs."
--
-- The default value `'false'` ensures defense-in-depth: any
-- connection that bypasses pgtenant.InAllTenants fails closed
-- because the GUC is unset / `'false'` by default.

CREATE OR REPLACE FUNCTION lenny_tenant_guard() RETURNS trigger AS $$
DECLARE
    ctx        TEXT := current_setting('app.current_tenant', true);
    allow_all  TEXT := current_setting('lenny.allow_all_sentinel', true);
    row_tenant TEXT;
BEGIN
    IF ctx IS NULL OR ctx = '' OR ctx = '__unset__' THEN
        RAISE EXCEPTION
            'lenny_tenant_guard: app.current_tenant is not set'
            USING ERRCODE = 'insufficient_privilege';
    END IF;

    IF ctx = '__all__' THEN
        -- spec: §4.2 line 165 — platform-admin cross-tenant
        -- sentinel requires explicit opt-in. The pgtenant.InAllTenants
        -- helper SETs lenny.allow_all_sentinel = 'true' via SET LOCAL;
        -- any other code path reaching the trigger with __all__ but
        -- no opt-in is rejected.
        IF allow_all IS NULL OR allow_all <> 'true' THEN
            RAISE EXCEPTION
                'lenny_tenant_guard: __all__ sentinel requires lenny.allow_all_sentinel = true (LENNY_POOLER_MODE rejected)'
                USING ERRCODE = 'insufficient_privilege';
        END IF;
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

-- spec: §4.2 line 165 — the __all__ sentinel in the RLS policies
-- must also require the lenny.allow_all_sentinel opt-in. Without
-- this, a SELECT that smuggled __all__ into app.current_tenant
-- (bypassing the trigger, which only fires on write) would still
-- read across tenants. Rewrite each lenny_tenant_isolation policy
-- to AND the __all__ bypass on the opt-in GUC.
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
                || '  OR ('
                || '    current_setting(''app.current_tenant'', false) = ''__all__'' '
                || '    AND current_setting(''lenny.allow_all_sentinel'', true) = ''true'''
                || '  )'
                || ')', t);
        END IF;
    END LOOP;
END $$;
