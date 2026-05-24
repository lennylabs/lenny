-- §4.4 line 293 mandates the `lenny_tenant_guard` trigger covers
-- session_eviction_state: "an RLS policy filters rows by
-- `current_setting('app.current_tenant', false)`, every access is
-- wrapped in a transaction with `SET LOCAL app.current_tenant`, and
-- the `lenny_tenant_guard` trigger covers this table."
--
-- Migration 0045 enabled RLS and the policy but did not install the
-- trigger; this migration closes the gap so a write attempt under a
-- bare connection without app.current_tenant raises an
-- insufficient_privilege error rather than silently failing the RLS
-- policy. The TestRLSTenantGuardMissingSetLocal integration test
-- enumerates every tenant-scoped table including session_eviction_state
-- and asserts the trigger rejects writes issued with no GUC.
--
-- spec: §4.4 line 293.

CREATE TRIGGER lenny_tenant_guard
    BEFORE INSERT OR UPDATE OR DELETE ON session_eviction_state
    FOR EACH ROW EXECUTE FUNCTION lenny_tenant_guard();
