-- §4.2 / §12.3 platform-admin __all__ cross-tenant bypass.
--
-- Extends lenny_tenant_guard() so that writes inside a transaction
-- that has set app.current_tenant = '__all__' bypass the per-row
-- tenant_id match. Adds the matching OR-clause to every
-- lenny_tenant_isolation RLS policy so SELECT-side reads also honor
-- the sentinel. The trigger continues to reject the unset, empty,
-- and '__unset__' values, and now also rejects values that do not
-- match the tenant-id format ^[A-Za-z0-9_-]{1,128}$ (§12.3 line 53).
--
-- The DB-level bypass trusts the gateway: only a code path that has
-- verified the caller holds the platform-admin role may set
-- '__all__'. Every such code path MUST emit a cross_tenant_read
-- audit event (§12.3 line 141) recording the caller identity, the
-- endpoint, and the query category.

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
        -- Platform-admin cross-tenant sentinel. The gateway sets
        -- this only after RBAC verifies the caller holds
        -- platform-admin and the gateway emits cross_tenant_read.
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

-- Update every lenny_tenant_isolation policy with the __all__ bypass.
-- The table list mirrors the tenant-scoped tables that carry the
-- policy across migrations 0002-0042. session_eviction_state (0045)
-- uses a separate policy name and current_setting(..., false) form
-- and is intentionally left untouched here.
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
            || 'USING ('
            || '  tenant_id = current_setting(''app.current_tenant'', true) '
            || '  OR current_setting(''app.current_tenant'', false) = ''__all__'''
            || ')', t);
    END LOOP;
END $$;
