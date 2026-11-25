DROP POLICY IF EXISTS lenny_tenant_isolation ON impersonation_starts;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON impersonation_starts;
DROP INDEX IF EXISTS idx_impersonation_starts_impersonator;
DROP TABLE IF EXISTS impersonation_starts;
