DROP POLICY IF EXISTS lenny_tenant_isolation ON session_checkpoint_meta;
DROP TRIGGER IF EXISTS lenny_tenant_guard ON session_checkpoint_meta;
DROP TABLE IF EXISTS session_checkpoint_meta;
