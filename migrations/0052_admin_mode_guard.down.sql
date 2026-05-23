-- Revert the §4.2 line 177 lenny.admin_mode guard.

DROP TRIGGER IF EXISTS lenny_admin_mode_required ON pool_tenant_access;
DROP TRIGGER IF EXISTS lenny_admin_mode_required ON runtime_tenant_access;
DROP FUNCTION IF EXISTS lenny_admin_mode_required();
