-- §4.2 line 177: defense-in-depth BEFORE INSERT/UPDATE/DELETE trigger
-- on runtime_tenant_access and pool_tenant_access. The trigger rejects
-- a write whose transaction has not set lenny.admin_mode = 'true'.
-- The gateway sets the GUC via SET LOCAL only on platform-admin code
-- paths after RBAC verification; a tenant-admin code path therefore
-- cannot reach these tables at the SQL layer even if application-layer
-- filtering is bypassed.
--
-- The trigger does not enforce a particular role — it trusts the
-- gateway to set the GUC only on a vetted code path. That layering is
-- consistent with the existing lenny_tenant_guard trigger, which
-- accepts whatever value app.current_tenant carries (the gateway is
-- responsible for setting it correctly).

CREATE FUNCTION lenny_admin_mode_required() RETURNS trigger AS $$
DECLARE
    mode TEXT := current_setting('lenny.admin_mode', true);
BEGIN
    IF mode IS NULL OR mode <> 'true' THEN
        RAISE EXCEPTION
            'lenny_admin_mode_required: lenny.admin_mode is not ''true''; % rejected on %', TG_OP, TG_TABLE_NAME
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER lenny_admin_mode_required
    BEFORE INSERT OR UPDATE OR DELETE ON runtime_tenant_access
    FOR EACH ROW EXECUTE FUNCTION lenny_admin_mode_required();

CREATE TRIGGER lenny_admin_mode_required
    BEFORE INSERT OR UPDATE OR DELETE ON pool_tenant_access
    FOR EACH ROW EXECUTE FUNCTION lenny_admin_mode_required();
