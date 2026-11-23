DROP INDEX IF EXISTS idx_issued_tokens_impersonator;
ALTER TABLE issued_tokens DROP COLUMN IF EXISTS impersonator_jti;
ALTER TABLE issued_tokens DROP COLUMN IF EXISTS impersonator_tenant_id;
